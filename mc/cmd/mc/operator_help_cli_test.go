package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc/operatorcmd"
)

func TestOperatorHelpCLICatalogMatchesDispatcher(t *testing.T) {
	spine := initSpine(t)
	env := spineEnv(spine)
	for _, definition := range operatorcmd.Definitions() {
		t.Run(definition.Name(), func(t *testing.T) {
			help := runMC(t, nil, "", append(definition.Path, "--help")...)
			if help.code != 0 {
				t.Fatalf("The command help failed: %s", help.stdout)
			}
			body, _ := json.Marshal(help.json["command"])
			var command operatorcmd.Definition
			if err := json.Unmarshal(body, &command); err != nil {
				t.Fatal(err)
			}
			if command.Name() != definition.Name() || command.Purpose == "" || command.Scope == "" || len(command.Result) == 0 {
				t.Fatalf("The command description is incomplete: %s", body)
			}
			checkHelpFields(t, command.Arguments)
			checkHelpFields(t, command.Flags)
			checkHelpFields(t, command.Request)
			checkHelpFields(t, command.Result)
			example := command.Examples[0]
			var request []byte
			if example.Request != nil {
				request, _ = json.Marshal(example.Request)
			}
			if err := operatorcmd.ValidatePublicRequest(example.Arguments, request); err != nil {
				t.Fatalf("The help example violates its public contract: %v", err)
			}
			if command.Name() == "agent-profile operation-start" {
				// An absent connection rejects the test before a process or provider call.
				request = []byte(`{"operation_id":"help-model-test","kind":"model_test","confirm_spend":true,"profile":{"id":"absent-profile","name":"Example","connection_id":"absent-connection","model":"absent-model"}}`)
			}
			if command.Name() == "project setup-preview" || command.Name() == "project setup-save" {
				value := example.Request.(map[string]any)
				value["path"] = filepath.Join(t.TempDir(), "absent-workspace")
				request, _ = json.Marshal(value)
			}
			result := runMC(t, env, string(request), example.Arguments...)
			if result.json == nil {
				t.Fatalf("The command returned no JSON object: %s", result.stderr)
			}
			lower := strings.ToLower(result.stdout + result.stderr)
			for _, unknown := range []string{"unknown verb", "unknown subverb", "unknown command", "unknown action", "action is unknown", "flag provided but not defined", "has no flag"} {
				if strings.Contains(lower, unknown) {
					t.Errorf("The dispatcher rejects its catalog: %s", result.stdout)
				}
			}
			if result.code == 0 {
				assertResultFields(t, command, result.json)
			}
		})
	}
}

func checkHelpFields(t *testing.T, fields []operatorcmd.Field) {
	t.Helper()
	for _, field := range fields {
		if field.Name == "" || field.Description == "" || !strings.Contains("|string|integer|number|boolean|array|object|value|", "|"+field.Type+"|") {
			t.Errorf("The field description is invalid: %+v", field)
		}
		checkHelpFields(t, field.Fields)
	}
}

func assertResultFields(t *testing.T, command operatorcmd.Definition, result map[string]any) {
	t.Helper()
	fields := map[string]bool{}
	for _, field := range command.Result {
		fields[field.Name] = true
	}
	for key := range result {
		if !fields[key] {
			t.Errorf("The %s help omits result field %s", command.Name(), key)
		}
	}
}

func TestOperatorHelpCLIVisionSubmissionContract(t *testing.T) {
	spine := initSpine(t)
	call := func(action string, request map[string]any) map[string]any {
		t.Helper()
		body, _ := json.Marshal(request)
		result := runMC(t, spineEnv(spine), string(body), "project", action)
		if result.code != 0 {
			t.Fatalf("%s failed: %s", action, result.stdout)
		}
		return result.json
	}
	draft := call("vision-open", map[string]any{"id": "ws-test"})["draft"].(map[string]any)
	draft = call("vision-edit", map[string]any{"id": "ws-test", "revision": draft["revision"], "content": "Help people inspect Culler command contracts."})["draft"].(map[string]any)
	result := call("vision-submit", map[string]any{"id": "ws-test", "revision": draft["revision"], "workspace_identity": draft["workspace_identity"], "workspace_revision": draft["workspace_revision"]})
	help := runMC(t, nil, "", "project", "vision-submit", "--help")
	body, _ := json.Marshal(help.json["command"])
	var command operatorcmd.Definition
	if err := json.Unmarshal(body, &command); err != nil {
		t.Fatal(err)
	}
	if result["draft"] == nil || result["submission"] == nil {
		t.Fatalf("The submission omitted its accepted state: %v", result)
	}
	assertResultFields(t, command, result)
}

func TestOperatorHelpCLIWriteContractAndRetry(t *testing.T) {
	spine := initSpine(t)
	request := `{"operation_id":"help-contract-create","action":"create","scope":"task","project":"ws-test","title":"Help contract task","description":"Verify the command contract.","priority":1}`
	first := runMC(t, spineEnv(spine), request, "board", "write")
	if first.code != 0 {
		t.Fatalf("Create failed: %s", first.stderr)
	}
	second := runMC(t, spineEnv(spine), request, "board", "write")
	if second.code != 0 || first.stdout != second.stdout {
		t.Fatalf("Retry changed the receipt: %s / %s", first.stdout, second.stdout)
	}
	help := runMC(t, nil, "", "board", "write", "--help")
	fields := map[string]bool{}
	for _, raw := range help.json["command"].(map[string]any)["result_fields"].([]any) {
		fields[raw.(map[string]any)["name"].(string)] = true
	}
	for key := range first.json {
		if !fields[key] {
			t.Errorf("The Board help omits result field %s", key)
		}
	}
	read := runMC(t, spineEnv(spine), "", "task", "get", fmt.Sprint(first.json["task_id"]))
	readHelp := runMC(t, nil, "", "task", "get", "--help")
	readFields := map[string]bool{}
	for _, raw := range readHelp.json["command"].(map[string]any)["result_fields"].([]any) {
		readFields[raw.(map[string]any)["name"].(string)] = true
	}
	for key := range read.json {
		if !readFields[key] {
			t.Errorf("The task read help omits result field %s", key)
		}
	}
	edit := map[string]any{"operation_id": "help-contract-edit", "action": "edit", "task_id": first.json["task_id"], "expected_version": 999, "title": "Wrong revision"}
	body, _ := json.Marshal(edit)
	refused := runMC(t, spineEnv(spine), string(body), "board", "write")
	if refused.code != 1 {
		t.Fatalf("The stale revision returned %d: %s", refused.code, refused.stdout)
	}
}

func TestOperatorHelpCLIWorksWithoutServicesOrFiles(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent-home")
	env := []string{"MC_HOME=" + home, "MC_SPINE=" + filepath.Join(home, "absent-spine.db"), "MC_HELPER=absent-helper", "PATH=/nonexistent"}
	for _, args := range [][]string{{"--help"}, {"project", "--help"}, {"project", "resume", "--help"}, {"environment", "save", "--help"}, {"agent-profile", "operation-start", "--help"}} {
		result := runMC(t, env, "", args...)
		if result.code != 0 {
			t.Fatalf("%v: exit %d: %s", args, result.code, result.stderr)
		}
		if result.json["format_version"] != float64(1) || result.json["release"] == "" {
			t.Fatalf("Missing help identity: %s", result.stdout)
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatalf("Help created deployment files: %v", err)
		}
	}
}

func TestOperatorHelpCLIContractsMatchRealReads(t *testing.T) {
	spine := initSpine(t)
	for _, args := range [][]string{{"task", "list"}, {"project", "list"}, {"packet", "list"}, {"run", "list"}, {"activity", "list"}, {"pause", "status"}} {
		result := runMC(t, spineEnv(spine), "", args...)
		if result.code != 0 {
			t.Fatalf("%v: %s", args, result.stderr)
		}
		help := runMC(t, nil, "", append(args, "--help")...)
		if help.code != 0 {
			t.Fatalf("Help %v: %s", args, help.stderr)
		}
		command := help.json["command"].(map[string]any)
		fields := map[string]bool{}
		for _, raw := range command["result_fields"].([]any) {
			fields[raw.(map[string]any)["name"].(string)] = true
		}
		for key := range result.json {
			if !fields[key] {
				t.Errorf("Help for %v omits actual result field %s", args, key)
			}
		}
	}
}

func TestOperatorHelpCLIArgumentsAndUnknownHelp(t *testing.T) {
	for _, args := range [][]string{{"project", "absent", "--help"}, {"missing", "--help"}, {"project", "resume", "--help", "extra"}} {
		result := runMC(t, nil, "", args...)
		if result.code != 2 {
			t.Fatalf("%v returned %d: %s", args, result.code, result.stdout)
		}
	}
	spine := initSpine(t)
	for _, check := range []struct {
		args []string
		help string
	}{
		{[]string{"missing"}, "mc --help"},
		{[]string{"project", "absent"}, "mc project --help"},
	} {
		result := runMC(t, spineEnv(spine), "", check.args...)
		if result.code != 2 || !strings.Contains(result.stdout, check.help) {
			t.Errorf("%v requires the nearest help path: %d %s", check.args, result.code, result.stdout)
		}
	}
}
