package onboarding

import (
	"reflect"
	"strings"
	"testing"
)

func TestAccessChoicePreservesAccountChoices(t *testing.T) {
	want := Session{Stage: 2, Access: &AccessChoice{Mode: "login", Step: "account-name", Harness: "codex", Template: "chatgpt", Binding: "second-account", Name: "second-account", NewAccount: true}}
	body, err := encodeSession(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeSession(body)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Account choices = %#v, %v", got, err)
	}
}

func TestAccessChoiceRejectsUnsupportedSavedConnections(t *testing.T) {
	for _, body := range []string{
		`{"stage":2,"access":{"mode":"static","step":"check"}}`,
		`{"stage":2,"access":{"mode":"login","step":"token"}}`,
		`{"stage":2,"access":{"mode":"login","step":"check","token":"fixture-secret"}}`,
		`{"stage":2,"access":{"mode":"login","step":"check","endpoint":"https://fixture-secret.example"}}`,
		`{"stage":2,"access":{"mode":"login","step":"check","path":"/fixture-secret"}}`,
		`{"stage":2,"static_bindings":["fixture-secret"]}`,
		`{"stage":2,"static_credentials":["fixture-secret"]}`,
	} {
		if _, err := decodeSession([]byte(body)); err == nil || strings.Contains(err.Error(), "fixture-secret") {
			t.Fatalf("Unsupported account result = %v", err)
		}
	}
}

func TestAccessChoiceRejectsUnsafeChoices(t *testing.T) {
	for name, update := range map[string]func(*AccessChoice){
		"method":     func(c *AccessChoice) { c.Mode = "unsupported" },
		"prompt":     func(c *AccessChoice) { c.Step = "unsupported" },
		"harness":    func(c *AccessChoice) { c.Harness = "unsupported" },
		"identifier": func(c *AccessChoice) { c.Binding = "../outside" },
	} {
		t.Run(name, func(t *testing.T) {
			choice := &AccessChoice{Mode: "login", Step: "check"}
			update(choice)
			body, err := encodeSession(Session{Stage: 2, Access: choice})
			if err == nil || len(body) != 0 {
				t.Fatalf("Unsafe account result = %q, %v", body, err)
			}
		})
	}
}
