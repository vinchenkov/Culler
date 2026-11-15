package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"mc/operatorcmd"
)

func TestOperatorHelpRequestContractsMatchDecoderFields(t *testing.T) {
	for _, d := range operatorcmd.Definitions() {
		target := operatorRequestTarget(d.Name())
		if target == nil {
			continue
		}
		actual := commandFields(reflect.TypeOf(target), 0)
		for _, f := range operatorcmd.RequestFields(d.Name()) {
			found := false
			for _, a := range actual {
				if a.Name == f.Name {
					found = true
					if a.Type != f.Type {
						t.Errorf("%s %s has type %s, decoder has %s", d.Name(), f.Name, f.Type, a.Type)
					}
				}
			}
			if !found {
				t.Errorf("%s describes absent decoder field %s", d.Name(), f.Name)
			}
		}
		description := describeOperatorCommand(d)
		body, err := json.Marshal(description.Examples[0].Request)
		if err != nil {
			t.Fatal(err)
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(target); err != nil {
			t.Errorf("%s has an invalid request example: %v", d.Name(), err)
		}
		args := description.Examples[0].Arguments
		if err := operatorcmd.ValidatePublicRequest(args, body); err != nil {
			t.Errorf("%s example fails public validation: %v", d.Name(), err)
		}
	}
}

func TestOperatorHelpReportsAuthorityWithoutLivePermission(t *testing.T) {
	t.Setenv("MC_RUN_JSON", t.TempDir()+"/absent.json")
	for _, args := range [][]string{{"--help"}, {"project", "--help"}, {"project", "resume", "--help"}} {
		var out, diagnostic bytes.Buffer
		handled, code := runOperatorHelp(args, &out, &diagnostic)
		if !handled || code != 0 {
			t.Fatalf("%v failed: %s", args, diagnostic.String())
		}
		var response map[string]any
		if json.Unmarshal(out.Bytes(), &response) != nil {
			t.Fatalf("Invalid help result %s", out.String())
		}
		authority := response["authority"].(map[string]any)
		if authority["live_check"] != "unavailable" || authority["state_check"] != "not_checked" {
			t.Fatalf("Help claims live permission: %v", authority)
		}
	}
}

func TestOperatorHelpValidatesTypedRequestBeforeEffects(t *testing.T) {
	for _, body := range []string{`null`, `{"changes":[],"identity":"host"}`, `{"changes":[]} {}`} {
		if _, err := validateOperatorCommand([]string{"config", "save"}, strings.NewReader(body)); err == nil {
			t.Fatalf("Accepted invalid payload %s", body)
		}
	}
}

func TestOperatorHelpStatesCredentialOperationScope(t *testing.T) {
	for _, action := range []string{"operation-status", "operation-cancel"} {
		var out, diagnostic bytes.Buffer
		handled, code := runOperatorHelp([]string{"agent-profile", action, "--help"}, &out, &diagnostic)
		if !handled || code != 0 {
			t.Fatalf("The operation help failed: %s", diagnostic.String())
		}
		var response struct {
			Command operatorcmd.Definition `json:"command"`
		}
		if err := json.Unmarshal(out.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		for _, required := range []string{"Collie", "model_test", "opencode_download", "Host callers", "credential authorization"} {
			if !strings.Contains(response.Command.Scope, required) {
				t.Errorf("%s omits %s from its authority scope", action, required)
			}
		}
	}
}

func TestOperatorHelpStatesVisionControlScope(t *testing.T) {
	for _, action := range []string{"pause", "resume"} {
		var out, diagnostic bytes.Buffer
		handled, code := runOperatorHelp([]string{action, "--help"}, &out, &diagnostic)
		if !handled || code != 0 {
			t.Fatalf("The control help failed: %s", diagnostic.String())
		}
		var response struct {
			Command operatorcmd.Definition `json:"command"`
		}
		if err := json.Unmarshal(out.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		requirements := strings.Join(response.Command.Requirements, " ")
		for _, required := range []string{"ordinary Collie conversation", "dedicated Vision conversation", "project " + action, "its own Project"} {
			if !strings.Contains(requirements, required) {
				t.Errorf("%s omits %s from its authority requirements", action, required)
			}
		}
	}
}
