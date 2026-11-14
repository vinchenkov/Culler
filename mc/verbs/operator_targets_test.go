package verbs

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestOperatorPublicReceiptRetainsProfileTargetWithoutInput(t *testing.T) {
	db := dvSpine(t)
	body := `{"id":"profile-one","revision":"current","name":"Private draft name","connection_id":"connection-one","model":"model-one"}`
	r, err := OperatorRequestSubmit(db, nil, "profile-audit", []string{"agent-profile", "save"}, body, "development", "external")
	if err != nil {
		t.Fatal(err)
	}
	public := r.Public()
	want := []OperatorTarget{{Kind: "profile", ID: "profile-one"}, {Kind: "connection", ID: "connection-one"}}
	if public.Target != "" || !reflect.DeepEqual(public.Targets, want) || public.Origin != "external" {
		t.Fatalf("the receipt lost its resource targets: %+v", public)
	}
	encoded, err := json.Marshal(public)
	if err != nil || strings.Contains(string(encoded), "Private draft name") || strings.Contains(string(encoded), `"input"`) {
		t.Fatalf("the public receipt exposed its input: %s, %v", encoded, err)
	}
}

func TestOperatorTargetsDescribeSettingsAssignmentsAndConversation(t *testing.T) {
	for _, tc := range []struct {
		args []string
		body string
		want []OperatorTarget
	}{
		{[]string{"config", "save"}, `{"changes":[{"key":"timing.timeout_minutes","value":20,"expected":10}]}`, []OperatorTarget{{"setting", "timing.timeout_minutes"}}},
		{[]string{"routing", "set", "--role", "worker", "--binding", "profile-one", "--expected", "profile-old"}, "", []OperatorTarget{{"role", "worker"}, {"profile", "profile-one"}}},
		{[]string{"connection", "retry"}, `{"connection_id":"connection-one","check_id":"check-one"}`, []OperatorTarget{{"connection", "connection-one"}, {"connection_check", "check-one"}}},
		{[]string{"collie", "title", "session-one", "--title", "Private title"}, "", []OperatorTarget{{"session", "session-one"}}},
		{[]string{"packet", "decide", "7", "--packet", "8", "--version", "9", "--approve"}, "", []OperatorTarget{{"task", "7"}, {"packet", "8"}, {"version", "9"}}},
		{[]string{"pause"}, "", []OperatorTarget{{"deployment", "all-projects"}}},
		{[]string{"task", "list"}, "", []OperatorTarget{}},
		{[]string{"task", "add", "42", "--project", "ws-test"}, "", []OperatorTarget{}},
	} {
		if got := operatorRequestTargets(OperatorRequest{Command: tc.args, Input: tc.body}); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%v targets = %+v, want %+v", tc.args, got, tc.want)
		}
	}
}

func TestOperatorReceiptAddsCreatedTaskAndKeepsOriginalProject(t *testing.T) {
	db := dvSpine(t)
	body := `{"operation_id":"create-target","action":"create","scope":"task","project":"ws-test","title":"Task"}`
	r, err := OperatorRequestSubmit(db, nil, "create-audit", []string{"board", "write"}, body, "development", "external")
	if err != nil {
		t.Fatal(err)
	}
	if r.Target != "ws-test" || !slices.Contains(r.Targets, OperatorTarget{"project", "ws-test"}) {
		t.Fatalf("the receipt lost its Project scope: %+v", r)
	}
	if _, err := OperatorRequestClaim(db, nil, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := OperatorRequestComplete(db, nil, r.ID, 0, []byte(`{"task_id":42,"operation_id":"create-target"}`), ""); err != nil {
		t.Fatal(err)
	}
	completed, err := OperatorRequestGet(db, nil, r.ID)
	if err != nil || !slices.Contains(completed.Public().Targets, OperatorTarget{"task", "42"}) {
		t.Fatalf("the receipt lost its created task: %+v, %v", completed, err)
	}
	if completed.Target != "ws-test" || completed.Input != body {
		t.Fatal("the completion changed the authorization scope or original request")
	}
}
