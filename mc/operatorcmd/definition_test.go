package operatorcmd

import (
	"slices"
	"strings"
	"testing"
)

func TestPublicBoundaryRejectsComponentCommands(t *testing.T) {
	for _, command := range []string{"dispatch", "environment report", "environment work", "agent-profile credential-read", "agent-profile operation-run", "connection observe", "project vision-complete", "collie reply", "run model-response"} {
		if _, ok := Lookup(strings.Fields(command)); ok {
			t.Fatalf("The operator catalog includes %s", command)
		}
	}
	for _, command := range []string{"project resume", "project setup-save", "environment approve", "agent-profile operation-start", "system resume-infrastructure", "routing set", "operation get"} {
		if _, ok := Lookup(strings.Fields(command)); !ok {
			t.Fatalf("The operator catalog omits %s", command)
		}
	}
	permissions := Permissions()
	if !slices.IsSorted(permissions) || !slices.Contains(permissions, "project.resume") || slices.Contains(permissions, "") {
		t.Fatalf("Invalid permission catalog: %v", permissions)
	}
}

func TestPublicRequestsRejectHostIdentityAndInternalInputs(t *testing.T) {
	cases := []struct {
		args []string
		body string
	}{
		{[]string{"environment", "retry"}, `{"project_id":"example","runtime_binding":{"id":"injected"}}`},
		{[]string{"environment", "save"}, `{"project_id":"example","expected_revision_id":"","configuration":{},"status":"succeeded"}`},
		{[]string{"project", "vision-submit"}, `{"id":"example","revision":"r","workspace_identity":"i","workspace_revision":"w","error":"injected"}`},
		{[]string{"agent-profile", "operation-start"}, `{"operation_id":"example","kind":"authorize"}`},
		{[]string{"agent-profile", "operation-start"}, `{"operation_id":"example","kind":"model_test","profile":{"id":"p","name":"P","connection_id":"c","model":"m"}}`},
		{[]string{"agent-profile", "operation-start"}, `{"operation_id":"example","kind":"model_test","confirm_spend":true,"profile":{"id":"p","name":"P","connection_id":"c","model":"m","base_url":"https://example.invalid"}}`},
		{[]string{"project", "setup-save"}, `{"id":"example","title":"Example","path":"/tmp/example","identity":"i","confirm_access":false}`},
		{[]string{"board", "write", "--request", `{"operation_id":"x","action":"create","actor":"host"}`}, ""},
		{[]string{"task", "get", "1"}, `{"identity":"host"}`},
	}
	for _, test := range cases {
		if err := ValidatePublicRequest(test.args, []byte(test.body)); err == nil {
			t.Errorf("Accepted unsafe request %v: %s", test.args, test.body)
		}
	}
}

func TestPublicRequestsPreserveSupportedInputForms(t *testing.T) {
	cases := []struct {
		args []string
		body string
	}{
		{[]string{"environment", "get", "--request", "-"}, `{"project_id":"example"}`},
		{[]string{"environment", "retry"}, `{"project_id":"example"}`},
		{[]string{"project", "vision-draft", "example"}, ""},
		{[]string{"project", "vision-edit", "example", "--revision", "r", "--content", ""}, ""},
		{[]string{"project", "vision-edit"}, `{"id":"example","revision":"r","content":""}`},
		{[]string{"project", "role-save", "--request", `{"project":"example","role":"worker","expected_revision":0,"instructions":""}`}, ""},
		{[]string{"board", "write", "--request", `{"operation_id":"x","action":"create","scope":"task","project":"example","title":"Example"}`}, ""},
		{[]string{"agent-profile", "operation-start"}, `{"operation_id":"x","kind":"opencode_download"}`},
		{[]string{"agent-profile", "operation-start"}, `{"operation_id":"x","kind":"model_test","confirm_spend":true,"profile":{"id":"p","name":"P","connection_id":"c","model":"m"}}`},
	}
	for _, test := range cases {
		if err := ValidatePublicRequest(test.args, []byte(test.body)); err != nil {
			t.Errorf("Refused supported request %v: %v", test.args, err)
		}
	}
}

func TestPublicRequestsKeepCredentialAuthorizationOnHost(t *testing.T) {
	args := []string{"agent-profile", "operation-start"}
	body := []byte(`{"operation_id":"authorization-check","kind":"authorization","connection_id":"example"}`)
	if err := ValidatePublicRequest(args, body); err == nil {
		t.Fatal("The public surface accepted credential authorization.")
	}
	if err := ValidateHostRequest(args, body); err != nil {
		t.Fatalf("The host lost credential authorization: %v", err)
	}
	if err := ValidateHostRequest(args, []byte(`{"operation_id":"authorization-check","kind":"authorization","connection_id":"example","identity":"host"}`)); err == nil {
		t.Fatal("The host request accepted an undeclared field.")
	}
}

func TestArgumentsUseDescribedValues(t *testing.T) {
	for _, args := range [][]string{
		{"project", "role-save", "-request", `{"project":"other","role":"worker","expected_revision":0,"instructions":"text"}`},
		{"packet", "decide", "1", "--approve", "--packet", "1", "--packet", "2", "--version", "1"},
		{"project", "role-save", "--project", "allowed", "--request", `{"project":"other","role":"worker","expected_revision":0,"instructions":"text"}`},
		{"packet", "decide", "1", "--approve", "--revise", "--packet", "1", "--version", "1"},
		{"packet", "decide", "1", "--approve", "--packet", "0", "--version", "1"},
		{"routing", "set", "--role", "worker", "--binding", "new"},
		{"task", "get", "-1"}, {"task", "list", "ignored"}, {"task", "get", "1", "--identity", "host"},
	} {
		d, ok := Lookup(args)
		if !ok {
			t.Fatalf("Missing test command %v", args)
		}
		if d.ValidateArguments(args) == nil {
			t.Errorf("Accepted invalid arguments %v", args)
		}
	}
	for _, d := range Definitions() {
		if err := d.ValidateArguments(d.Examples[0].Arguments); err != nil {
			t.Errorf("Invalid example for %s: %v", d.Name(), err)
		}
	}
}

func TestArgumentsKeepPacketDecisionDomainRequirements(t *testing.T) {
	args := []string{"packet", "decide", "1", "--approve"}
	d, ok := Lookup(args)
	if !ok {
		t.Fatal("The packet decision command is absent.")
	}
	if err := d.ValidateArguments(args); err != nil {
		t.Fatalf("The argument check replaced a packet domain refusal: %v", err)
	}
	for _, name := range []string{"packet", "version"} {
		found := false
		for _, field := range d.Flags {
			if field.Name == name {
				found = field.Required && field.RequirementCheck == "domain"
			}
		}
		if !found {
			t.Errorf("The help omits the required domain check for %s", name)
		}
	}
}
