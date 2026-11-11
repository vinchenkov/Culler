package verbs

import (
	"encoding/json"
	"slices"
	"strconv"
)

// OperatorTarget identifies an affected resource without the request contents.
type OperatorTarget struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// The receipt retains every source field. Derived targets also include identifiers
// that a successful creation returns after admission.
func operatorRequestTargets(r OperatorRequest) []OperatorTarget {
	targets := []OperatorTarget{}
	add := func(kind, id string) {
		value := OperatorTarget{Kind: kind, ID: id}
		if id != "" && !slices.Contains(targets, value) {
			targets = append(targets, value)
		}
	}
	addNumber := func(kind string, id int64) {
		if id > 0 {
			add(kind, strconv.FormatInt(id, 10))
		}
	}
	add("project", r.Target)
	if len(r.Command) == 0 {
		return targets
	}
	input := r.Input
	if inline := operatorArgument(r.Command, "request"); inline != "" && inline != "-" {
		input = inline
	}
	var body struct {
		ID           string `json:"id"`
		TaskID       int64  `json:"task_id"`
		Run          string `json:"run"`
		Role         string `json:"role"`
		ConnectionID string `json:"connection_id"`
		CheckID      string `json:"check_id"`
		OperationID  string `json:"operation_id"`
		SubmissionID string `json:"submission_id"`
		Profile      struct {
			ID           string `json:"id"`
			ConnectionID string `json:"connection_id"`
			SubmissionID string `json:"submission_id"`
		} `json:"profile"`
		Changes []struct {
			Key string `json:"key"`
		} `json:"changes"`
	}
	_ = json.Unmarshal([]byte(input), &body)
	action := ""
	if len(r.Command) > 1 {
		action = r.Command[1]
	}
	switch r.Command[0] {
	case "pause", "resume":
		if action == "" {
			add("deployment", "all-projects")
		}
	case "task", "packet":
		if action != "add" && len(r.Command) > 2 {
			task, _ := strconv.ParseInt(r.Command[2], 10, 64)
			addNumber("task", task)
		}
		add("packet", operatorArgument(r.Command, "packet"))
		add("version", operatorArgument(r.Command, "version"))
	case "board":
		addNumber("task", body.TaskID)
		add("board_operation", body.OperationID)
	case "project":
		add("role", body.Role)
		add("run", body.Run)
		add("vision_submission", body.SubmissionID)
	case "agent-profile":
		if action == "save" {
			add("profile", body.ID)
			add("profile_submission", body.SubmissionID)
		}
		add("profile", body.Profile.ID)
		add("profile_submission", body.Profile.SubmissionID)
		add("connection", body.ConnectionID)
		add("connection", body.Profile.ConnectionID)
		add("agent_profile_operation", body.OperationID)
	case "connection":
		add("connection", body.ConnectionID)
		add("connection_check", body.CheckID)
	case "routing":
		add("role", operatorArgument(r.Command, "role"))
		add("profile", operatorArgument(r.Command, "binding"))
	case "config":
		for _, change := range body.Changes {
			add("setting", change.Key)
		}
	case "collie":
		if len(r.Command) > 2 {
			add("session", r.Command[2])
		}
	}
	if r.ExitCode != nil && *r.ExitCode == 0 {
		var result struct {
			TaskID       int64  `json:"task_id"`
			ProfileID    string `json:"profile_id"`
			ConnectionID string `json:"connection_id"`
			CheckID      string `json:"check_id"`
			OperationID  string `json:"operation_id"`
		}
		if json.Unmarshal(r.Result, &result) == nil {
			addNumber("task", result.TaskID)
			add("profile", result.ProfileID)
			add("connection", result.ConnectionID)
			add("connection_check", result.CheckID)
			if r.Command[0] == "agent-profile" {
				add("agent_profile_operation", result.OperationID)
			}
		}
	}
	return targets
}
