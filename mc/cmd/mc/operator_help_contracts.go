package main

import (
	"reflect"
	"strings"

	"mc/operatorcmd"
	"mc/verbs"
)

func completeOperatorDescription(d operatorcmd.Definition) operatorcmd.Definition {
	set := func(names ...string) { d.Result = namedFields(names...) }
	switch d.Name() {
	case "task block":
		set("task_id", "blocked", "superseded")
	case "task unblock":
		set("task_id", "blocked", "revision_id", "revision_state")
	case "task resume":
		set("task_id", "resumed")
	case "task cancel":
		set("task_id", "cancelled", "run_id", "stop_pending")
	case "task interrupt":
		set("task_id", "run_id", "cancelled", "stop_pending")
	case "board write":
		set("operation_id", "action", "task_id", "scope", "record_version", "blocked", "cancelled", "revision_id", "revision_state")
	case "board history-report":
		set("task_id", "record_id", "render_path", "report_sha256", "artifact_retention")
	case "packet decide":
		set("task_id", "decision", "archived")
	case "project setup-preview":
		set("workspace", "git_author")
	case "project setup-save":
		set("project_id", "status", "draft")
	case "project details":
		set("project", "workspace", "git_author")
	case "project vision-workspace":
		set("workspace")
	case "project vision-save":
		set("vision")
	case "project vision-open", "project vision-draft", "project vision-edit", "project vision-rebase", "project vision-apply", "project start":
		set("draft")
	case "project vision-submit":
		set("draft", "submission")
	case "project vision-submission":
		set("submission")
	case "project role-show":
		set("current", "contract", "project_title", "roles", "history", "runs", "conflicts")
	case "project role-save", "run customization":
		d.Result = commandFields(reflect.TypeOf(verbs.RoleCustomization{}), 0)
	case "project role-unblock":
		set("run", "blocked")
	case "config show", "config save":
		set("config_version", "families")
	case "collie title":
		set("session_id", "title", "changed")
	case "collie history":
		set("session_id", "status", "messages")
	case "collie end":
		set("session_id", "status", "ended")
	case "collie draft":
		set("session_id", "message_id", "bytes")
	case "collie packet-context":
		set("session_id", "packet_id", "version_id", "context")
	case "activity list":
		set("activity", "limit")
	case "routing show", "routing set":
		set("routes", "bindings")
	case "system diagnostics":
		set("events", "next_cursor", "refresh_outcomes")
	case "system status":
		d.Result = append(commandFields(reflect.TypeOf(verbs.SystemStatusResult{}), 0), operatorcmd.Field{Name: "viewers", Type: "object", Description: "The current viewer availability.", Fields: commandFields(reflect.TypeOf(verbs.Viewers{}), 0)})
	case "system resume-infrastructure":
		set("project_id", "resumed")
	case "system resolve-proposals":
		set("project_id", "decision_id", "resolved")
	case "connection check-start":
		set("check_id", "operation", "allowed", "reason", "next_retry_at", "authorization_id", "deadline")
	case "connection retry":
		set("resumed")
	case "connection check-cancel":
		set("cleared")
	case "operation get", "operation list":
		for i := range d.Result {
			for _, f := range commandFields(reflect.TypeOf(verbs.OperatorRequest{}), 0) {
				if f.Name != "identity" && f.Name != "permission" && f.Name != "input" && f.Name != "claimed" {
					d.Result[i].Fields = append(d.Result[i].Fields, f)
				}
			}
		}
	}
	if strings.HasPrefix(d.Name(), "environment ") {
		set("environment", "draft", "selected", "revisions", "operations", "results", "approvals", "decisions", "vision", "run_evidence", "base_source", "inventory", "disk_use")
		for i := range d.Result {
			var value any
			switch d.Result[i].Name {
			case "environment":
				value = verbs.EnvironmentState{}
			case "draft", "selected", "revisions":
				value = verbs.EnvironmentRevision{}
			case "operations":
				value = verbs.EnvironmentOperation{}
			}
			if value != nil {
				d.Result[i].Fields = commandFields(reflect.TypeOf(value), 0)
			}
		}
		if d.Name() == "environment save" {
			d.Asynchronous = false
			d.StatusRead = "mc environment get < JSON request"
		}
	}
	if strings.HasPrefix(d.Name(), "logs ") && d.Name() != "logs runs" {
		if d.Name() == "logs run" {
			set("run_id", "exists", "format", "tail", "truncated", "size_bytes", "lines", "malformed", "records", "rendered")
		} else {
			set("unit", "stream", "exists", "format", "tail", "truncated", "size_bytes", "lines", "malformed", "records", "rendered")
		}
	}
	for i := range d.Result {
		if d.Result[i].Name == "draft" && strings.HasPrefix(d.Name(), "project ") {
			d.Result[i].Fields = commandFields(reflect.TypeOf(verbs.VisionDraft{}), 0)
		}
		if d.Result[i].Name == "submission" {
			d.Result[i].Fields = commandFields(reflect.TypeOf(verbs.VisionSubmission{}), 0)
		}
		if d.Result[i].Name == "workspace" {
			d.Result[i].Fields = commandFields(reflect.TypeOf(verbs.WorkspacePreview{}), 0)
		}
		if d.Result[i].Name == "vision" && d.Name() == "project vision-save" {
			d.Result[i].Fields = commandFields(reflect.TypeOf(verbs.WorkspaceVision{}), 0)
		}
	}
	if example := operatorResultExample(d.Name()); example != nil {
		d.Examples[0].Result = example
	}
	if request := completeRequestExample(d.Name()); request != nil {
		d.Examples[0].Request = request
	}
	if d.Name() == "project vision-submit" {
		d.Retry = "The Project, draft revision, workspace identity, and workspace revision identify the same submission. Read the returned submission before retry."
		d.Requirements = []string{"The current draft and workspace revisions match. The draft contains the Project goal.", "The response contains the generated submission_id. Use it for vision-submission and vision-apply."}
	}
	for i := range d.Flags {
		f := &d.Flags[i]
		switch f.Name {
		case "tail":
			f.Default = 200
		case "format":
			f.Default = "text"
		case "all", "live", "follow":
			f.Default = false
		case "limit":
			if d.Name() == "activity list" {
				f.Default = 100
			}
		case "stream":
			if !f.Required {
				f.Default = "stderr"
			}
			if d.Name() == "logs agentsview" {
				f.Default = "debug"
			}
		case "request":
			if strings.HasPrefix(d.Name(), "project role-") {
				f.Default = "-"
			}
		}
	}
	if len(d.Examples[0].Result.(map[string]any)) == 0 {
		d.Examples[0].Result = resultShapeExample(d.Result)
	}
	return d
}

func resultShapeExample(fields []operatorcmd.Field) map[string]any {
	result := map[string]any{}
	for _, field := range fields {
		switch field.Type {
		case "string":
			result[field.Name] = "example"
		case "integer", "number":
			result[field.Name] = 0
		case "boolean":
			result[field.Name] = false
		case "array":
			result[field.Name] = []any{}
		case "object":
			result[field.Name] = resultShapeExample(field.Fields)
		default:
			result[field.Name] = nil
		}
	}
	return result
}

func completeRequestExample(name string) any {
	base := map[string]any{"id": "example"}
	switch name {
	case "project vision-edit":
		base["revision"] = "current-draft-revision"
		base["content"] = "# Vision\nDeliver the approved Project goal."
	case "project vision-rebase", "project vision-submit":
		base["revision"] = "current-draft-revision"
		base["workspace_identity"] = "current-workspace-identity"
		base["workspace_revision"] = "current-workspace-revision"
	case "project vision-submission", "project vision-apply":
		base["submission_id"] = "current-submission-id"
	case "project vision-save":
		base["identity"] = "current-workspace-identity"
		base["revision"] = "current-workspace-revision"
		base["vision"] = "# Vision\nDeliver the approved Project goal."
	case "project start":
		base["identity"] = "current-workspace-identity"
		base["revision"] = "current-draft-revision"
		base["submission_id"] = "current-submission-id"
	case "environment approve", "environment deny":
		return map[string]any{"project_id": "example", "revision_id": "current-proposed-revision"}
	case "environment assist":
		return map[string]any{"project_id": "example", "instruction": "Prepare the declared Project dependencies."}
	case "environment save":
		return map[string]any{"project_id": "example", "expected_revision_id": "current-draft-revision", "configuration": map[string]any{"base_digest": "sha256:" + strings.Repeat("a", 64), "dockerfile": nil, "build_files": []any{}, "build_args": map[string]string{}, "setup": map[string]string{"command": "npm ci", "directory": "."}, "services": []any{}, "variables": []any{}, "secrets": []any{}, "declarations": []string{"package-lock.json"}}, "explanation": "Prepare the declared dependencies."}
	case "agent-profile save":
		return map[string]any{"submission_id": "example-save-1", "revision": "current-catalog-revision", "id": "example", "name": "Example", "connection_id": "example", "model": "model-from-live-catalog", "enabled": true, "test_id": "successful-matching-test"}
	default:
		return nil
	}
	return base
}

func operatorResultExample(name string) any {
	switch name {
	case "task list":
		return map[string]any{"tasks": []any{}}
	case "packet list":
		return map[string]any{"packets": []any{}}
	case "project list":
		return map[string]any{"projects": []any{}}
	case "run list":
		return map[string]any{"runs": []any{}}
	case "activity list":
		return map[string]any{"activity": []any{}, "limit": 100}
	case "collie list":
		return map[string]any{"sessions": []any{}}
	case "operation list":
		return map[string]any{"operations": []any{}}
	case "logs runs":
		return map[string]any{"runs": []any{}}
	case "pause", "pause status", "resume":
		return map[string]any{"projects": []any{}}
	case "task block":
		return map[string]any{"task_id": 1, "blocked": true, "superseded": false}
	case "task unblock":
		return map[string]any{"task_id": 1, "blocked": false}
	case "task resume":
		return map[string]any{"task_id": 1, "resumed": true}
	case "task cancel":
		return map[string]any{"task_id": 1, "cancelled": true}
	case "task interrupt":
		return map[string]any{"task_id": 1, "run_id": "example-run", "cancelled": true, "stop_pending": true}
	case "project pause":
		return map[string]any{"project_id": "example", "status": "paused"}
	case "project archive":
		return map[string]any{"project_id": "example", "status": "archived"}
	case "project workspaces":
		return map[string]any{"workspaces": []any{}}
	case "project readiness":
		return map[string]any{"ready": true, "issues": []any{}, "profiles": []any{}}
	case "project delete-preview", "project delete":
		return map[string]any{"project_id": "example", "title": "Example", "tasks": 0, "reviews": 0, "setup_operations": 0, "blockers": []any{}}
	case "packet decide":
		return map[string]any{"task_id": 1, "decision": "approved", "archived": false}
	case "board history-report":
		return map[string]any{"task_id": 1, "record_id": 1, "render_path": nil, "artifact_retention": "unavailable"}
	case "collie title":
		return map[string]any{"session_id": "example", "title": "Inspect Project state", "changed": true}
	case "collie history":
		return map[string]any{"session_id": "example", "status": "active", "messages": []any{}}
	case "collie end":
		return map[string]any{"session_id": "example", "status": "ended", "ended": true}
	case "collie draft":
		return map[string]any{"session_id": "example", "message_id": 1, "bytes": 0}
	case "project role-unblock":
		return map[string]any{"run": "example", "blocked": false}
	case "agent-profile models":
		return map[string]any{"connection_id": "example", "models": []any{}, "web_search": false}
	}
	return nil
}
