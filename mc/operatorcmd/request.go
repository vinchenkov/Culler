package operatorcmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

// RequestFields defines the public payload. Component requests use their own contracts.
func RequestFields(name string) []Field {
	id := textField("id", "The Project identifier.", true)
	project := textField("project_id", "The Project identifier.", true)
	revision := textField("revision", "The current revision from the preceding read.", true)
	identity := textField("identity", "The current workspace identity from the preceding preview.", true)
	workspaceIdentity := textField("workspace_identity", "The current workspace identity from the preceding preview.", true)
	workspaceRevision := textField("workspace_revision", "The current workspace Vision revision from the preceding preview.", true)
	submission := textField("submission_id", "The submission identifier returned by the draft read.", true)
	operation := textField("operation_id", "The stable operation identifier. Reuse it only with the exact request.", true)
	operation.MaxBytes = 128
	content := textField("content", "The complete Vision draft text. An empty draft is permitted.", true)
	content.MaxBytes = 65536
	switch name {
	case "board write":
		action := choice("action", "The task action.", true, "create", "edit", "revise", "block", "unblock", "resume", "cancel", "allow-corrections")
		task := integer("task_id", "The task identifier.", false, 1)
		task.RequiredWhen = "action is not create"
		version := integer("expected_version", "The record_version from task get.", false, 1)
		version.RequiredWhen = "action is not create"
		scope := choice("scope", "The type of the new task.", false, "task")
		scope.RequiredWhen = "action is create"
		p := textField("project", "The Project for the new task.", false)
		p.RequiredWhen = "action is create"
		title := textField("title", "The task title without control characters.", false)
		title.MaxBytes = 4096
		title.RequiredWhen = "action is create"
		priority := integer("priority", "The priority. Lower values run first.", false, -1)
		maximum := int64(3)
		priority.Maximum = &maximum
		description := textField("description", "The complete task requirements.", false)
		reason := textField("reason", "The explicit decision and its reason.", false)
		reason.RequiredWhen = "action is block, unblock, resume, cancel, or allow-corrections"
		return []Field{operation, action, task, version, scope, p, title, priority, description, reason, textField("instructions", "The revision instructions. Required when the title and description stay unchanged.", false), integer("packet_id", "The reviewed packet, when the request comes from Review.", false, 1), integer("version_id", "The submitted version of the reviewed packet.", false, 1)}
	case "project setup-preview":
		return []Field{textField("path", "The absolute Project folder path.", true)}
	case "project setup-save":
		confirm := boolean("confirm_access", "The operator confirms access to this Project folder.")
		confirm.Required = true
		title := textField("title", "The Project title without control characters.", true)
		title.MaxBytes = 200
		return []Field{id, title, textField("path", "The canonical absolute folder path from setup-preview.", true), identity, boolean("create", "Confirm creation when the folder is absent."), confirm,
			textField("revision", "The preview's Vision revision. Registration reads the current Vision.", false)}
	case "project details", "project vision-open", "project vision-workspace", "project vision-draft":
		return []Field{id}
	case "project vision-save":
		vision := textField("vision", "The complete replacement workspace Vision.", true)
		vision.MaxBytes = 65536
		return []Field{id, identity, revision, vision}
	case "project vision-apply", "project vision-submission":
		return []Field{id, submission}
	case "project start":
		submission.RequirementCheck = "domain"
		return []Field{id, identity, revision, submission}
	case "project vision-edit":
		return []Field{id, revision, content}
	case "project vision-rebase", "project vision-submit":
		return []Field{id, revision, workspaceIdentity, workspaceRevision}
	case "project role-save":
		instructions := textField("instructions", "The complete custom instructions. Empty text removes the custom instructions.", true)
		instructions.MaxBytes = 65536
		return []Field{textField("project", "The Project identifier.", true), choice("role", "The pipeline role.", true, "strategist(propose)", "arbiter", "worker", "verifier", "packager", "scribe"), instructions}
	case "project role-unblock":
		return []Field{textField("run", "The run with the independent role conflict.", true), textField("reason", "The operator's conflict resolution.", true)}
	case "environment get", "environment retry", "environment cache-clear":
		return []Field{project}
	case "environment approve", "environment deny":
		return []Field{project, textField("revision_id", "The exact proposed revision from environment get.", true)}
	case "environment assist":
		instruction := textField("instruction", "The requested environment change.", true)
		instruction.MaxBytes = 8192
		return []Field{project, instruction}
	case "environment save":
		expected := textField("expected_revision_id", "The current draft revision; use an empty string for the initial draft.", true)
		explanation := textField("explanation", "The reason for the environment configuration.", false)
		explanation.MaxBytes = 8192
		return []Field{project, expected, field("configuration", "object", "The complete environment configuration, including its selected base image.", true), explanation}
	case "config save":
		changes := field("changes", "array", "One or more changes. Each supported setting appears once.", true)
		changes.Fields = []Field{textField("key", "The supported key from config show.", true), field("value", "value", "The replacement value of the setting's documented type and range.", true), field("expected", "value", "The exact current value from config show.", true)}
		return []Field{changes}
	case "agent-profile models":
		return []Field{textField("connection_id", "The configured connection identifier.", true)}
	case "agent-profile save":
		return profileFields(true)
	case "agent-profile operation-start":
		operation.Description = "The model test identifier. OpenCode returns its own operation identifier; use --request-id to retain the request."
		kind := choice("kind", "The operator operation.", true, "model_test", "opencode_download")
		profile := field("profile", "object", "The unsaved profile draft for the model test.", false)
		profile.RequiredWhen = "kind is model_test"
		profile.Fields = profileFields(false)
		confirm := boolean("confirm_spend", "The operator authorizes provider expenditure for this model test.")
		confirm.RequiredWhen = "kind is model_test"
		return []Field{operation, kind, profile, confirm, textField("connection_id", "The optional connection identifier associated with the operation.", false)}
	case "agent-profile operation-status", "agent-profile operation-cancel":
		return []Field{operation}
	case "connection check-start", "connection retry", "connection check-cancel":
		check := textField("check_id", "The stable check identifier: 32 lowercase hexadecimal characters.", name != "connection check-start")
		return []Field{textField("connection_id", "The configured connection identifier.", true), check, textField("profile_id", "The optional profile to check.", false), textField("profile_revision", "The current profile revision when profile_id is present.", false), textField("authorization_id", "The current authorization. The host resolves it for a new check.", false), textField("operation", "The check operation from current system status.", false)}
	}
	return nil
}

func profileFields(save bool) []Field {
	fields := []Field{
		textField("submission_id", "The stable identifier for a new profile. Reuse it only with the exact request.", false),
		textField("revision", "The current catalog revision.", save),
		textField("id", "The existing profile identifier. Omit it for a new profile with submission_id.", false), textField("name", "The profile display name. It contains no spaces.", true),
		textField("connection_id", "The configured connection identifier.", true), textField("model", "A model from the live catalog for this connection.", true),
		textField("effort", "An effort value from this model's live catalog entry.", false), boolean("enabled", "Enable this verified profile for role assignment."),
		textField("test_id", "The successful model test for this exact execution configuration and authorization.", false),
	}
	fields[len(fields)-1].RequiredWhen = "save enables or changes the execution configuration"
	fields[0].RequiredWhen = "id is absent"
	fields[0].MaxBytes = 128
	fields[2].MaxBytes = 64
	fields[3].MaxBytes = 128
	return fields
}

// ValidatePublicRequest applies the public payload boundary at authenticated admission.
func ValidatePublicRequest(args []string, body []byte) error {
	return validateRequest(args, body, false)
}

// ValidateHostRequest preserves the host's credential authorization operation.
func ValidateHostRequest(args []string, body []byte) error {
	return validateRequest(args, body, true)
}

func validateRequest(args []string, body []byte, host bool) error {
	d, ok := Lookup(args)
	if !ok {
		return fmt.Errorf("The operator command is unsupported.")
	}
	if err := d.ValidateArguments(args); err != nil {
		return err
	}
	rules := RequestFields(d.Name())
	if host && d.Name() == "agent-profile operation-start" {
		for i := range rules {
			if rules[i].Name == "kind" {
				rules[i].Values = append(rules[i].Values, "authorization")
			}
		}
	}
	if rules == nil {
		if len(bytes.TrimSpace(body)) > 0 {
			return fmt.Errorf("The command accepts no JSON request.")
		}
		return nil
	}
	if d.Name() == "project vision-draft" || d.Name() == "project vision-edit" {
		if len(args) > len(d.Path) {
			if len(bytes.TrimSpace(body)) > 0 {
				return fmt.Errorf("The Vision command accepts arguments or JSON, not both.")
			}
			return nil
		}
	}
	if d.Name() == "board write" || strings.HasPrefix(d.Name(), "project role-") {
		for i, arg := range args {
			value := ""
			if arg == "--request" && i+1 < len(args) {
				value = args[i+1]
			}
			if strings.HasPrefix(arg, "--request=") {
				value = strings.TrimPrefix(arg, "--request=")
			}
			if value != "" && value != "-" {
				body = []byte(value)
			}
		}
	}
	if d.RequestLimit > 0 && int64(len(body)) > d.RequestLimit {
		return fmt.Errorf("The request exceeds %d bytes.", d.RequestLimit)
	}
	var value map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil || value == nil {
		return fmt.Errorf("The request requires one JSON object.")
	}
	if dec.Decode(new(any)) != io.EOF {
		return fmt.Errorf("The request requires one JSON object.")
	}
	if err := validateObject(rules, value); err != nil {
		return err
	}
	if d.Name() == "agent-profile operation-start" && value["kind"] == "model_test" {
		if value["confirm_spend"] != true || value["profile"] == nil {
			return fmt.Errorf("A model test requires a profile and explicit expenditure authorization.")
		}
	}
	if d.Name() == "project setup-save" && value["confirm_access"] != true {
		return fmt.Errorf("Project registration requires explicit access confirmation.")
	}
	return nil
}

func validateObject(fields []Field, object map[string]any) error {
	for key, value := range object {
		index := slices.IndexFunc(fields, func(f Field) bool { return f.Name == key })
		if index < 0 {
			return fmt.Errorf("The operator request does not accept %s.", key)
		}
		f := fields[index]
		if value == nil {
			return fmt.Errorf("The %s field cannot be null.", key)
		}
		switch f.Type {
		case "string":
			text, ok := value.(string)
			if !ok {
				return fmt.Errorf("The %s field requires text.", key)
			}
			if f.MaxBytes > 0 && len(text) > f.MaxBytes {
				return fmt.Errorf("The %s field exceeds %d bytes.", key, f.MaxBytes)
			}
			if len(f.Values) > 0 && !slices.Contains(f.Values, text) {
				return fmt.Errorf("The %s field accepts %s.", key, strings.Join(f.Values, ", "))
			}
		case "integer":
			number, ok := value.(json.Number)
			if !ok {
				return fmt.Errorf("The %s field requires an integer.", key)
			}
			if err := validateValue(f, number.String()); err != nil {
				return err
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("The %s field requires a boolean.", key)
			}
		case "object":
			nested, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("The %s field requires an object.", key)
			}
			if len(f.Fields) > 0 {
				if err := validateObject(f.Fields, nested); err != nil {
					return err
				}
			}
		case "array":
			items, ok := value.([]any)
			if !ok {
				return fmt.Errorf("The %s field requires an array.", key)
			}
			if len(f.Fields) > 0 {
				for _, item := range items {
					nested, ok := item.(map[string]any)
					if !ok {
						return fmt.Errorf("Each %s item requires an object.", key)
					}
					if err := validateObject(f.Fields, nested); err != nil {
						return err
					}
				}
			}
		}
	}
	for _, f := range fields {
		if _, ok := object[f.Name]; f.Required && f.RequirementCheck != "domain" && !ok {
			return fmt.Errorf("The request requires %s.", f.Name)
		}
	}
	return nil
}
