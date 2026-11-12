package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"

	"mc/deployment"
	"mc/operatorcmd"
	"mc/verbs"
)

func annotateOperatorUsage(args []string, err error) error {
	var usage *verbs.UsageError
	if !errors.As(err, &usage) || strings.Contains(usage.Error(), "--help") {
		return err
	}
	if len(args) > 0 && strings.HasPrefix(args[0], "__") {
		return err
	}
	message := strings.ToLower(usage.Error())
	if !strings.HasPrefix(message, "unknown ") && !strings.Contains(message, "action is unknown") {
		return err
	}
	if id, loadErr := verbs.LoadIdentity(); loadErr != nil || id != nil && id.Tier != "collie" {
		return err
	}
	return verbs.Usagef("%s Read %s.", usage.Error(), operatorcmd.NearestHelp(args))
}

type commandAuthority struct {
	Caller     string `json:"caller"`
	Permission string `json:"permission"`
	LiveCheck  string `json:"live_check"`
	StateCheck string `json:"state_check"`
	Reason     string `json:"reason"`
}

// Help returns before logs, deployment discovery, or helper execution.
func runOperatorHelp(args []string, stdout, stderr io.Writer) (bool, int) {
	index := slices.Index(args, "--help")
	if index < 0 {
		return false, 0
	}
	path := args[:index]
	if index != len(args)-1 {
		return true, writeVerbError(stdout, stderr, verbs.Usagef("Help accepts a command path followed by --help."))
	}
	response := map[string]any{"format_version": operatorcmd.FormatVersion, "release": releaseBuildID,
		"exit_codes":       map[string]int{"success": 0, "domain_rejection": 1, "usage_or_environment": 2},
		"contract":         "Each command returns one JSON object on stdout. Diagnostics use stderr.",
		"request_identity": "A write accepts --request-id <id> for the retained operator request. Reuse the identifier and exact request after a timeout.",
		"authority":        helpAuthority(nil),
		"transport": map[string]any{
			"completed": "The command returns its domain result when host execution completes within the wait.",
			"accepted":  "A longer host request returns operation, accepted=true, and complete=false. Read operation get <request-id> for its result.",
			"states":    []string{"pending", "active", "complete", "uncertain"},
			"timeout":   "A timeout does not prove failure. Inspect the accepted request before another submission.",
		}}
	if len(path) == 0 {
		families := []map[string]any{}
		seen := map[string]bool{}
		for _, d := range operatorcmd.Definitions() {
			name := d.Path[0]
			if seen[name] {
				continue
			}
			seen[name] = true
			families = append(families, map[string]any{"name": name, "purpose": familyPurpose(name), "help": "mc " + name + " --help"})
		}
		response["families"] = families
	} else {
		var exact *operatorcmd.Definition
		commands := []map[string]any{}
		for _, value := range operatorcmd.Definitions() {
			if len(value.Path) < len(path) || !slices.Equal(path, value.Path[:len(path)]) {
				continue
			}
			d := describeOperatorCommand(value)
			if len(d.Path) == len(path) {
				exact = &d
			}
			commands = append(commands, map[string]any{"path": d.Path, "purpose": d.Purpose, "mutates": d.Mutates, "authority": helpAuthority(&d), "help": "mc " + d.Name() + " --help"})
		}
		if exact != nil {
			response["command"] = exact
			response["authority"] = helpAuthority(exact)
		}
		if len(commands) > 1 || exact == nil {
			response["commands"] = commands
		}
		if len(commands) == 0 {
			return true, writeVerbError(stdout, stderr, verbs.Usagef("The operator command is unknown. Read %s.", operatorcmd.NearestHelp(path)))
		}
	}
	if err := writeJSON(stdout, response); err != nil {
		return true, writeVerbError(stdout, stderr, err)
	}
	return true, 0
}

func helpAuthority(d *operatorcmd.Definition) commandAuthority {
	a := commandAuthority{Caller: "unavailable", Permission: "unavailable", LiveCheck: "unavailable", StateCheck: "not_checked",
		Reason: "Static help checks no spine or service. Execution checks current authority and target state."}
	id, err := verbs.LoadIdentity()
	if err != nil {
		a.Reason = "The caller identity is unavailable. Static command descriptions remain available."
		return a
	}
	if id == nil {
		a.Caller, a.Permission = "operator_account", "host_account"
		return a
	}
	a.Caller = id.Tier
	if d == nil {
		a.Permission = "command_specific"
		return a
	}
	if id.Tier != "collie" {
		a.Permission, a.Reason = "role_specific", "The role contract controls access. The operator catalog grants no role authority."
		return a
	}
	if d.Permission == "" {
		a.Permission = "read_scope"
		return a
	}
	if slices.Contains(id.VerbAllowlist, d.Permission) {
		a.Permission = "present_in_launch"
	} else {
		a.Permission = "restricted"
	}
	return a
}

func validateOperatorCommand(args []string, stdin io.Reader) (io.Reader, error) {
	d, found := operatorcmd.Lookup(args)
	if !found {
		return stdin, nil
	}
	if err := d.ValidateArguments(args); err != nil {
		return nil, verbs.Usagef("%v", err)
	}
	target := operatorRequestTarget(d.Name())
	if target == nil {
		return stdin, nil
	}
	if (d.Name() == "project vision-draft" || d.Name() == "project vision-edit") && len(args) > len(d.Path) {
		return stdin, nil
	}
	// Existing inline request parsers retain their domain-specific validation.
	if d.Name() == "board write" || strings.HasPrefix(d.Name(), "project role-") {
		return stdin, nil
	}
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	limit := d.RequestLimit
	if limit == 0 {
		limit = 1024 * 1024
	}
	body, err := io.ReadAll(io.LimitReader(stdin, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, verbs.Usagef("The request exceeds %d bytes or cannot be read.", limit)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return nil, verbs.Usagef("The %s request is invalid: %v", d.Name(), err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, verbs.Usagef("The request requires one JSON object.")
	}
	var object map[string]any
	if json.Unmarshal(body, &object) != nil || object == nil {
		return nil, verbs.Usagef("The request requires one JSON object.")
	}
	return bytes.NewReader(body), nil
}

func operatorRequestTarget(name string) any {
	switch name {
	case "board write":
		return new(verbs.BoardWriteRequest)
	case "project role-save":
		return new(verbs.RoleSaveRequest)
	case "project role-unblock":
		return new(verbs.RoleUnblockRequest)
	case "project setup-preview", "project setup-save", "project details", "project vision-save", "project vision-open", "project vision-workspace", "project vision-apply", "project start":
		return new(projectSetupRequest)
	case "project vision-draft", "project vision-edit", "project vision-rebase", "project vision-submit", "project vision-submission":
		return new(verbs.VisionDraftRequest)
	case "environment get", "environment save", "environment approve", "environment deny", "environment assist", "environment retry", "environment cache-clear":
		return new(verbs.EnvironmentRequest)
	case "config save":
		return new(verbs.ConfigSaveRequest)
	case "agent-profile models":
		return new(deployment.AgentModelListRequest)
	case "agent-profile save":
		return new(deployment.AgentProfileRequest)
	case "agent-profile operation-start":
		return new(deployment.AgentProfileOperationRequest)
	case "agent-profile operation-status", "agent-profile operation-cancel":
		return &struct {
			OperationID string `json:"operation_id"`
		}{}
	case "connection check-start", "connection retry", "connection check-cancel":
		return new(verbs.ConnectionCheck)
	}
	return nil
}

func describeOperatorCommand(d operatorcmd.Definition) operatorcmd.Definition {
	if target := operatorRequestTarget(d.Name()); target != nil {
		d.Request = operatorcmd.RequestFields(d.Name())
		for i := range d.Request {
			if len(d.Request[i].Fields) != 0 {
				continue
			}
			for _, actual := range commandFields(reflect.TypeOf(target), 0) {
				if d.Request[i].Name == actual.Name {
					d.Request[i].Fields = actual.Fields
				}
			}
		}
		d.Examples[0].Request = requestExample(d.Name())
	}
	switch d.Name() {
	case "agent-profile list", "agent-profile save":
		d.Result = commandFields(reflect.TypeOf(deployment.AgentProfileCatalog{}), 0)
	case "agent-profile models":
		d.Result = commandFields(reflect.TypeOf(deployment.AgentModelList{}), 0)
	case "agent-profile operation-start", "agent-profile operation-status", "agent-profile operation-cancel":
		d.Result = commandFields(reflect.TypeOf(deployment.AgentProfileOperation{}), 0)
	case "pause", "resume", "pause status":
		d.Result = commandFields(reflect.TypeOf(verbs.PipelinePauseStatus{}), 0)
	case "project delete-preview", "project delete":
		d.Result = commandFields(reflect.TypeOf(verbs.ProjectDeletion{}), 0)
	case "task get":
		d.Result = namedFields("id", "title", "description", "strategic_rationale", "scope", "priority", "record_version", "requirements_version", "created_at", "status", "stage_rank", "stage_entered_at", "correction_count", "blocked", "blocked_reason", "blocked_kind", "dispatch_retries", "run_retry_at", "stalled", "infrastructure_failure_count", "decision", "decided_at", "archived", "origin", "project", "completion_sha", "verified_sha", "base_commit", "revision_notes", "latest_infrastructure_failure", "connection_wait", "project_infrastructure", "proposal_decision_id", "delivery", "requirements_history", "pending_revision", "board_history", "run_history", "edit_restriction", "run_requirements", "artifacts", "evidence_history", "packet_history")
	case "project readiness":
		d.Result = commandFields(reflect.TypeOf(projectReadiness{}), 0)
	case "collie list":
		d.Result = namedFields("sessions")
	case "logs run":
		d.Result = namedFields("run_id", "path", "exists", "text", "records")
	case "health":
		d.Result = namedFields("ok", "availability", "setup", "log_evidence", "tail", "findings", "attention")
	}
	if d.Name() == "task get" {
		d.Examples[0].Result = map[string]any{"id": 1, "title": "Example task", "record_version": 1, "status": "proposed", "project": "example"}
	}
	if d.Name() == "project resume" {
		d.Examples[0].Result = map[string]any{"project_id": "example", "status": "active"}
	}
	if d.Name() == "board write" {
		d.Examples[0].Result = map[string]any{"operation_id": "example-1", "action": "create", "task_id": 1}
	}
	if d.Name() == "agent-profile operation-start" {
		d.Examples[0].Result = map[string]any{"operation_id": "opencode-download", "kind": "opencode_download", "status": "pending", "started_at": "2026-11-11T12:00:00Z"}
	}
	return completeOperatorDescription(d)
}

func commandFields(t reflect.Type, depth int) []operatorcmd.Field {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || depth > 6 {
		return nil
	}
	var fields []operatorcmd.Field
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if f.Anonymous && name == "" {
			fields = append(fields, commandFields(f.Type, depth+1)...)
			continue
		}
		if name == "" {
			name = f.Name
		}
		kind := f.Type
		for kind.Kind() == reflect.Pointer {
			kind = kind.Elem()
		}
		label := "object"
		switch kind.Kind() {
		case reflect.String:
			label = "string"
		case reflect.Bool:
			label = "boolean"
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			label = "integer"
		case reflect.Float32, reflect.Float64:
			label = "number"
		case reflect.Slice, reflect.Array:
			label = "array"
		case reflect.Interface:
			label = "value"
		}
		if f.Type == reflect.TypeOf(json.RawMessage{}) {
			label = "value"
		}
		fields = append(fields, operatorcmd.Field{Name: name, Type: label, Description: fieldDescription(name), Fields: commandFields(f.Type, depth+1)})
	}
	return fields
}

func namedFields(names ...string) []operatorcmd.Field {
	fields := make([]operatorcmd.Field, 0, len(names))
	for _, name := range names {
		fields = append(fields, operatorcmd.Field{Name: name, Type: "value", Description: fieldDescription(name)})
	}
	return fields
}

func fieldDescription(name string) string {
	if text, ok := map[string]string{
		"operation_id":         "The stable operation identifier. Reuse it only with the exact request.",
		"submission_id":        "The stable submission identifier. Reuse it only with the exact request.",
		"revision":             "The current revision from the preceding read.",
		"expected_revision_id": "The current environment revision identifier.", "expected_version": "The displayed task record_version.",
		"confirm_spend": "Explicit authorization for the model test's provider expenditure.", "confirm_access": "Explicit authorization for Culler to manage this Project folder.",
		"test_id":            "The successful test for this exact profile configuration and credential authorization.",
		"kind":               "The operation kind. Operator profile operations accept model_test or opencode_download.",
		"record_version":     "The version for the next task replacement or decision.",
		"status":             "The current state. An accepted asynchronous operation can remain incomplete.",
		"action":             "create, edit, revise, block, unblock, resume, cancel, or allow-corrections.",
		"priority":           "The task priority from -1 through 3.",
		"workspace_identity": "The identity from the current workspace preview.",
		"workspace_revision": "The revision of the current workspace Vision.",
		"identity":           "The identity from the current workspace preview.",
		"check_id":           "The stable 32-character lowercase hexadecimal check identifier.",
		"authorization_id":   "The current connection authorization. The host resolves this value for a new check.",
		"runtime_binding":    "The host-owned runtime binding. Operator requests leave this field absent.",
	}[name]; ok {
		return text
	}
	return "The " + strings.ReplaceAll(name, "_", " ") + " for this record."
}

func requestExample(name string) any {
	switch {
	case name == "board write":
		return map[string]any{"operation_id": "example-1", "action": "create", "scope": "task", "project": "example", "title": "Example task", "description": "Implement the approved task.", "priority": 1}
	case name == "config save":
		return map[string]any{"changes": []any{map[string]any{"key": "timing.timeout_minutes", "expected": 30, "value": 45}}}
	case name == "agent-profile models":
		return map[string]any{"connection_id": "example"}
	case name == "agent-profile operation-start":
		return map[string]any{"operation_id": "example-1", "kind": "opencode_download"}
	case strings.HasPrefix(name, "agent-profile operation-"):
		return map[string]any{"operation_id": "example-1"}
	case strings.HasPrefix(name, "connection "):
		return map[string]any{"connection_id": "example", "check_id": "0123456789abcdef0123456789abcdef"}
	case strings.HasPrefix(name, "environment "):
		return map[string]any{"project_id": "example"}
	case name == "project setup-preview":
		return map[string]any{"path": "/Users/operator/Projects/example"}
	case name == "project setup-save":
		return map[string]any{"id": "example", "title": "Example", "path": "/Users/operator/Projects/example", "identity": "<current workspace identity>", "confirm_access": true}
	case name == "project role-save":
		return map[string]any{"project": "example", "role": "worker", "instructions": "Use the Project test command."}
	case name == "project role-unblock":
		return map[string]any{"run": "example", "reason": "The operator resolved the instruction conflict."}
	case strings.HasPrefix(name, "project "):
		return map[string]any{"id": "example"}
	}
	return map[string]any{}
}

func familyPurpose(name string) string {
	return map[string]string{
		"task": "Inspect and control tasks.", "board": "Submit task requests and inspect task evidence.", "packet": "Inspect Review Packets and submit exact-version decisions.",
		"project": "Register Projects and manage their state, Vision, and role instructions.", "run": "Inspect pipeline runs and their role instructions.", "activity": "Read retained system activity.",
		"pause": "Inspect or pause Project pipelines.", "resume": "Resume initialized Project pipelines.", "environment": "Manage Project dependencies, image decisions, and setup operations.",
		"system": "Inspect deployment state and resolve supported system conditions.", "connection": "Request or cancel connection checks.", "config": "Read and replace supported system settings.",
		"agent-profile": "Inspect models, test profiles, and save verified profiles.", "routing": "Read and select role profiles.", "logs": "Read bounded diagnostic logs.",
		"health": "Read host component health.", "collie": "Manage the authorized conversation.", "operation": "Inspect retained operator requests and their results.",
	}[name]
}
