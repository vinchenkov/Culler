package operatorcmd

import (
	"slices"
	"strings"
)

func field(name, kind, description string, required bool) Field {
	return Field{Name: name, Type: kind, Description: description, Required: required}
}

func textField(name, description string, required bool) Field {
	return field(name, "string", description, required)
}
func integer(name, description string, required bool, minimum int64) Field {
	f := field(name, "integer", description, required)
	f.Minimum = &minimum
	return f
}
func boolean(name, description string) Field { return field(name, "boolean", description, false) }
func choice(name, description string, required bool, values ...string) Field {
	f := textField(name, description, required)
	f.Values = values
	return f
}

func definitions() []Definition {
	var all []Definition
	add := func(name, purpose string, write, host bool, positional []Field, flags ...Field) *Definition {
		d := Definition{Path: strings.Fields(name), Purpose: purpose, Mutates: write, Host: host,
			Scope: "The selected records in this deployment.", Arguments: positional, Flags: flags,
			Result: []Field{}, Requirements: []string{"The caller has command authority. The target exists and permits the requested operation."},
			Refusals: []string{"The caller lacks authority, the target changed, or its current state refuses the operation."},
			Retry:    "Repeat a read to obtain current state."}
		if d.Arguments == nil {
			d.Arguments = []Field{}
		}
		if d.Flags == nil {
			d.Flags = []Field{}
		}
		if write {
			d.Permission = strings.ReplaceAll(name, " ", ".")
			d.Retry = "Read the target after an uncertain result. Reuse the request identifier and exact request when supplied."
		}
		all = append(all, d)
		return &all[len(all)-1]
	}
	result := func(d *Definition, names ...string) *Definition {
		for _, name := range names {
			d.Result = append(d.Result, field(name, "value", "The current "+strings.ReplaceAll(name, "_", " ")+".", false))
		}
		return d
	}
	request := func(d *Definition, maximum int64) *Definition {
		d.RequestSource, d.RequestLimit = "One JSON object on stdin.", maximum
		return d
	}
	task := []Field{integer("task", "The task identifier.", true, 1)}
	project := []Field{textField("project", "The Project identifier.", true)}
	session := []Field{textField("session", "The Collie session identifier.", true)}
	result(add("task get", "Read a task, its version, and its current conditions.", false, false, task), "task")
	result(add("task revision", "Read one revision through bounded views and evidence pointers.", false, false, task,
		integer("revision", "The revision identifier.", true, 1), choice("view", "The revision view.", false, "summary", "instructions", "earlier-instructions", "changes", "evidence"),
		integer("offset", "The next character offset from a previous page.", false, 0)), "revision_id", "view")

	result(add("task list", "List current tasks, with optional Project and archive filters.", false, false, nil,
		boolean("all", "Include archived tasks."), textField("project", "Restrict the result to this Project.", false)), "tasks")
	for _, action := range []string{"block", "unblock", "resume", "cancel"} {
		d := result(add("task "+action, "Apply the "+action+" decision to a task.", true, false, task, textField("reason", "The operator decision and its reason.", true)), "task_id")
		d.StatusRead = "mc task get <task>"
	}
	result(add("task interrupt", "Request that the active task run stop.", true, false, task), "task_id")
	d := request(result(add("board write", "Create or change a task with a retained operation receipt.", true, false, nil, textField("request", "The request as JSON. Standard input supplies the default.", false)), "operation_id", "action", "task"), 1024*1024)
	d.Retry = "Reuse operation_id and the exact request. A different request with that identifier is refused."
	d.Requirements = []string{"Creation requires scope=task, project, and title. Priority is -1 through 3.", "Other actions require task_id and expected_version. A revision accepts title, description, and optional instructions.", "Block, unblock, resume, cancel, and allow-corrections require reason. Edit accepts title or priority."}
	result(add("board history-report", "Read the packet report or evidence of a task history record.", false, false, nil,
		integer("task", "The task identifier.", true, 1), integer("record", "The history record identifier.", true, 1), choice("kind", "The report kind.", true, "packet", "evidence")), "report")
	d = result(add("packet list", "Read Review Packets and their current decision and merge states.", false, false, nil,
		boolean("live", "Select packets with a retained review slot, including approved or returned packets."), integer("task", "Select the latest packet of this task.", false, 1)), "packets")
	d.Flags[1].MinimumMessage = "mc packet list --task requires a positive task identifier"
	d = result(add("packet decide", "Approve, revise, or cancel the exact reviewed packet version.", true, false, task,
		boolean("approve", "Approve the reviewed packet."), boolean("revise", "Return the packet for revision."), boolean("cancel", "Cancel the reviewed packet."),
		textField("reason", "Required for revise and cancel; forbidden for approve.", false), integer("packet", "The reviewed packet identifier.", true, 1), integer("version", "The reviewed version identifier.", true, 1)), "task_id", "decision")
	d.OneOf = []string{"approve", "revise", "cancel"}
	for i := range d.Flags {
		if d.Flags[i].Name == "packet" || d.Flags[i].Name == "version" {
			d.Flags[i].RequirementCheck = "domain"
		}
	}
	d.Requirements = []string{"The packet and version match the current packet that awaits a decision.", "Revise and cancel require a reason. Approve forbids a reason."}
	d.Asynchronous, d.StatusRead = true, "mc packet list --task <task>"
	d.Retry = "Read the current packet after an uncertain decision. Approval accepts a merge request; merged state confirms its completion."
	result(add("project list", "List Projects and their current states.", false, false, nil), "projects")
	for _, action := range []string{"pause", "resume", "archive"} {
		d = result(add("project "+action, "Set the Project to the "+action+" state.", true, false, project), "project_id", "status")
		d.Requirements = []string{"Resume requires complete Project initialization. Independent task and infrastructure conditions remain in effect."}
	}
	result(add("project delete-preview", "Inspect the exact effects of Project deletion.", false, true, project), "project_id", "title", "workspace_root")
	d = result(add("project delete", "Delete a Project after confirmation of its current preview.", true, true, project, boolean("confirm", "Confirm deletion after the current preview.")), "project_id", "deleted")
	d.Flags[0].Required = true
	d.Requirements = []string{"The operator confirms the current deletion preview. The command preserves the Project repository."}
	for _, action := range []string{"workspaces", "readiness"} {
		result(add("project "+action, "Inspect Project "+action+" on the host.", false, true, nil), action)
	}
	for _, action := range []string{"setup-preview", "setup-save", "details", "vision-save", "vision-open", "vision-workspace", "vision-apply", "start"} {
		write := slices.Contains([]string{"setup-save", "vision-save", "vision-open", "vision-apply", "start"}, action)
		d = request(result(add("project "+action, projectPurpose(action), write, true, nil), "project", "workspace", "draft"), 128*1024)
		d.Requirements = []string{"The request identifies the Project or workspace. A write uses the current workspace identity and revision.", "Registration requires title and confirm_access. Creation of an absent folder also requires create.", "Start requires a valid Vision and complete agent and environment readiness."}
	}
	for _, action := range []string{"vision-draft", "vision-edit", "vision-rebase", "vision-submit", "vision-submission"} {
		var pos []Field
		var flags []Field
		if action == "vision-draft" || action == "vision-edit" {
			pos = []Field{textField("project", "The Project identifier. Omit it to use a JSON request.", false)}
			flags = []Field{textField("revision", "The expected draft revision.", false), textField("content", "The replacement draft text.", false)}
		}
		d = request(result(add("project "+action, projectPurpose(action), action != "vision-draft" && action != "vision-submission", false, pos, flags...), "draft", "submission"), 128*1024)
		d.Permission = "project." + action
		d.Requirements = []string{"The Project exists. A dedicated Vision conversation targets its own Project."}
		if action == "vision-edit" || action == "vision-rebase" || action == "vision-submit" {
			d.Requirements = append(d.Requirements, "The expected draft revision matches the current draft.")
		}
		if action == "vision-rebase" || action == "vision-submit" {
			d.Requirements = append(d.Requirements, "The workspace identity and revision match the current workspace.")
		}
		if action == "vision-submission" {
			d.Requirements = append(d.Requirements, "The submission_id comes from the accepted Vision submission.")
		}
		if action == "vision-submit" {
			d.Asynchronous, d.StatusRead = true, "mc project vision-submission < JSON request"
		}
	}
	roleFlags := []Field{textField("project", "The Project identifier.", true), choice("role", "The pipeline role.", true, "strategist(propose)", "arbiter", "worker", "verifier", "packager", "scribe"), textField("request", "The request as JSON; - reads standard input.", false)}
	for _, action := range []string{"role-show", "role-save", "role-unblock"} {
		d = result(add("project "+action, "Inspect or change the Project's role instructions and independent role block.", action != "role-show", false, nil, roleFlags...), "customization")
		if action != "role-show" {
			d.Flags = []Field{textField("request", "The request as JSON; - reads stdin.", false)}
			request(d, 1024*1024)
		}
	}
	result(add("run list", "Read pipeline runs, their state, and trace references.", false, false, nil, integer("task", "Restrict to this task.", false, 0), textField("project", "Restrict to this Project.", false)), "runs")
	result(add("run customization", "Read the exact role instructions and conflicts of a run.", false, false, []Field{textField("run", "The run identifier.", true)}), "customization", "conflict")
	result(add("activity list", "Read the newest activity records.", false, false, nil, textField("kind", "Restrict to this activity kind.", false), textField("project", "Restrict to this Project.", false), integer("limit", "The maximum number of records.", false, 0)), "activity")
	result(add("pause status", "Read the global Project pause state.", false, false, nil), "projects")
	d = result(add("pause", "Pause every active Project in one transaction.", true, false, nil), "projects")
	d.Requirements = []string{"An ordinary Collie conversation can pause Projects with its frozen permission.", "A dedicated Vision conversation uses project pause for its own Project."}
	d = result(add("resume", "Resume paused Projects with complete initialization.", true, false, nil), "projects")
	d.Requirements = []string{"Projects with incomplete initialization remain paused. Independent conditions remain in effect.", "An ordinary Collie conversation can resume Projects with its frozen permission.", "A dedicated Vision conversation uses project resume for its own Project."}
	for _, action := range []string{"get", "save", "approve", "deny", "assist", "retry", "cache-clear"} {
		d = request(result(add("environment "+action, environmentPurpose(action), action != "get", action == "save" || action == "retry", nil, choice("request", "Read the JSON request from standard input.", false, "-")), "environment", "selected", "draft", "operations"), 3*1024*1024)
		d.Permission = "environment." + action
		d.Requirements = []string{"The request identifies project_id. A replacement names expected_revision_id.", "Image decisions identify the exact revision_id. The selected image must pass runtime compatibility checks."}
		if action != "get" {
			d.Asynchronous, d.StatusRead = true, "mc environment get < JSON request"
		}
	}
	result(add("system status", "Read current deployment availability, connections, and infrastructure conditions.", false, true, nil), "complete", "availability", "connections", "projects", "viewers")
	result(add("system diagnostics", "Read retained diagnostic evidence and recovery records.", false, true, nil,
		textField("project", "Restrict to this Project.", false), textField("connection", "Restrict to this connection.", false), textField("run", "Restrict to this run.", false), textField("attempt", "Restrict to this reply attempt.", false), integer("after", "The history cursor.", false, 0)), "diagnostics")
	d = result(add("system resume-infrastructure", "Request a fresh infrastructure check for a Project.", true, true, project, textField("request", "The stable check identifier.", true)), "check_id")
	d.Asynchronous, d.StatusRead = true, "mc system status"
	result(add("system resolve-proposals", "Resolve the current shared proposal decision.", true, true, project, integer("decision", "The shared proposal decision identifier.", true, 1), textField("reason", "The decision resolution.", true)), "project", "decision")
	for _, action := range []string{"check-start", "retry", "check-cancel"} {
		d = request(result(add("connection "+action, "Request or cancel a check of the current connection.", true, true, nil), "check_id", "status"), 64*1024)
		d.Asynchronous, d.StatusRead = true, "mc system status"
		d.Retry = "Reuse check_id and the exact request. Read system status before a new check after an uncertain result."
	}
	result(add("config show", "Read supported system settings and their current values.", false, true, nil), "settings")
	d = request(result(add("config save", "Replace selected settings with expected-value checks.", true, true, nil), "settings"), 64*1024)
	d.Requirements = []string{"Each change supplies key, value, and expected. The current value matches expected or value."}
	result(add("agent-profile list", "Read profiles, connections, live model availability, and the configuration revision.", false, true, nil), "revision", "profiles", "connections", "providers", "integrations", "opencode")
	for _, action := range []string{"models", "save", "operation-start", "operation-status", "operation-cancel"} {
		d = request(result(add("agent-profile "+action, profilePurpose(action), action == "save" || action == "operation-start" || action == "operation-cancel", true, nil), "operation_id", "status"), 64*1024)
		if action == "operation-start" {
			d.Requirements = []string{"kind is model_test or opencode_download. Model tests require confirm_spend=true.", "Model tests use the supplied profile draft and preserve the saved profile's verification.", "Use the returned operation_id for operation-status and operation-cancel."}
			d.Asynchronous, d.StatusRead = true, "mc agent-profile operation-status < JSON request"
			d.Retry = "Reuse --request-id and the exact request after uncertainty. Model tests also retain operation_id. OpenCode returns opencode-download; a new request can restart a completed download."
		}
		if action == "save" {
			d.Requirements = []string{"The configuration revision matches. Enabled profiles require test evidence for the exact execution configuration and credential authorization."}
		}
		if action == "operation-status" || action == "operation-cancel" {
			d.Scope = "Collie can inspect or cancel model_test and opencode_download operations. Host callers retain credential authorization operations."
		}
	}
	result(add("routing show", "Read the runtime profile assigned to each role.", false, true, nil), "routes")
	result(add("routing set", "Assign an enabled and valid profile to one role.", true, true, nil, choice("role", "The role to configure.", true, "strategist", "arbiter", "worker", "verifier", "packager", "scribe", "collie"), textField("binding", "The enabled profile identifier.", true), textField("expected", "The current binding from routing show.", true)), "routes")
	for _, unit := range []string{"resident", "dashboard", "agentsview", "mc"} {
		streams := []string{"stdout", "stderr", "events"}
		if unit == "agentsview" {
			streams = []string{"stdout", "stderr", "debug"}
		}
		if unit == "mc" {
			streams = []string{"events"}
		}
		result(add("logs "+unit, "Read bounded host logs with credential masks.", false, true, nil, choice("stream", "The component log stream.", unit == "mc", streams...), integer("tail", "The line count; default 200, clamped to the retained limit.", false, 0), choice("format", "The record format; default text.", false, "text", "json")), "unit", "stream", "lines")
	}
	result(add("logs run", "Read the bounded diagnostic log of one run.", false, true, []Field{textField("run", "The run identifier, never a file path.", true)}, integer("tail", "The line count; default 200, clamped.", false, 0), choice("format", "The record format; default text.", false, "text", "json")), "run", "lines")
	d = result(add("logs runs", "List runs with retained diagnostic logs.", false, true, nil, boolean("list", "Select the list of available runs.")), "runs")
	d.Flags[0].Required = true
	d.OneOf = []string{"list"}
	result(add("health", "Read host component health from retained log records.", false, true, nil, integer("tail", "The records per component; default 200, clamped.", false, 0)), "components")
	for _, action := range []string{"title", "draft", "history", "list", "end", "packet-context"} {
		pos := session
		var flags []Field
		switch action {
		case "title":
			flags = []Field{textField("title", "The conversation title.", true)}
		case "draft":
			flags = []Field{textField("attempt", "The current reply attempt.", false), integer("to", "The claimed message identifier.", true, 1), textField("body", "The provisional reply text.", false)}
		case "history":
			flags = []Field{boolean("follow", "Wait for the next reply within the existing deadline.")}
		case "list":
			pos = nil
		case "end":
			flags = []Field{textField("reason", "The reason for the session end.", true)}
		}
		d = result(add("collie "+action, "Use the "+action+" operation for the authorized Collie conversation.", slices.Contains([]string{"title", "draft", "end"}, action), false, pos, flags...), "session")
		d.Permission, d.Scope = "collie."+action, "Collie can access its own session. The host retains its existing conversation scope."
		if action == "packet-context" {
			d.Permission = ""
		}
	}
	result(add("operation get", "Read a retained operator request after acceptance or a timeout.", false, false, []Field{textField("request-id", "The accepted request identifier.", true)}), "operation")
	result(add("operation list", "List retained operator requests in the caller's scope.", false, false, nil), "operations")
	for i := range all {
		d := &all[i]
		if len(d.Examples) == 0 {
			d.Examples = []Example{{Arguments: exampleArguments(*d), Result: map[string]any{}}}
		}
	}
	return all
}

func exampleArguments(d Definition) []string {
	args := slices.Clone(d.Path)
	for _, f := range d.Arguments {
		if !f.Required {
			continue
		}
		value := "example"
		if f.Type == "integer" {
			value = "1"
		}
		args = append(args, value)
	}
	for _, f := range d.Flags {
		if !f.Required {
			continue
		}
		if f.Type == "boolean" {
			args = append(args, "--"+f.Name)
			continue
		}
		value := "example"
		if f.Type == "integer" {
			value = "1"
		}
		if len(f.Values) > 0 {
			value = f.Values[0]
		}
		args = append(args, "--"+f.Name, value)
	}
	if len(d.OneOf) > 0 && !slices.Contains(args, "--"+d.OneOf[0]) {
		args = append(args, "--"+d.OneOf[0])
	}
	return args
}

func projectPurpose(action string) string {
	return map[string]string{
		"setup-preview": "Inspect a workspace before Project registration.", "setup-save": "Register the confirmed workspace and initialize its Vision draft.",
		"details": "Read a Project and its current workspace identity.", "vision-save": "Save the confirmed Vision text in an initialized workspace.",
		"vision-open": "Read or initialize the persistent Vision draft.", "vision-workspace": "Read the current workspace Vision and identity.",
		"vision-apply": "Apply an accepted Vision submission to the workspace.", "start": "Accept the initialized Project source and start development.",
		"vision-draft": "Read the current persistent Vision draft.", "vision-edit": "Replace the Vision draft at its expected revision.",
		"vision-rebase": "Reconcile the Vision draft with the current workspace.", "vision-submit": "Submit the exact Vision draft for a workspace save.",
		"vision-submission": "Read the state and result of a Vision submission.",
	}[action]
}

func environmentPurpose(action string) string {
	return map[string]string{
		"get": "Read the Project environment, image decisions, operations, and setup reports.", "save": "Save an environment configuration at the expected revision.",
		"approve": "Approve the exact proposed Project image configuration.", "deny": "Deny the exact proposed image and retain the selected image.",
		"assist": "Request a setup agent for the Project environment.", "retry": "Retry the applicable environment operation.",
		"cache-clear": "Request removal of disposable Project dependency caches.",
	}[action]
}

func profilePurpose(action string) string {
	return map[string]string{
		"models": "Read the live model catalog for a connection.", "save": "Save a profile at the expected configuration revision.",
		"operation-start": "Start a model test or the existing OpenCode download.", "operation-status": "Read a model test or download operation.",
		"operation-cancel": "Cancel an applicable profile operation.",
	}[action]
}
