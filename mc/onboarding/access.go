package onboarding

import (
	"fmt"
	"strings"
	"unicode"
)

// AccessChoice holds account choices and the current prompt. Credentials stay outside the setup record.
type AccessChoice struct {
	Mode           string `json:"mode"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	Step           string `json:"step"`
	Harness        string `json:"harness,omitempty"`
	Template       string `json:"template,omitempty"`
	Binding        string `json:"binding,omitempty"`
	Name           string `json:"name,omitempty"`
	NewAccount     bool   `json:"new_account,omitempty"`
	CreatedBinding string `json:"created_binding,omitempty"`
}

func (choice AccessChoice) validate() error {
	if choice.Mode != "login" && choice.Mode != "api_key" {
		return fmt.Errorf("the saved access method is invalid")
	}
	validStep := choice.Step == "account-name" || choice.Step == "check"
	if choice.Mode == "api_key" {
		validStep = choice.Step == "provider" || choice.Step == "key-choice" || choice.Step == "key" || choice.Step == "model" || choice.Step == "effort" || choice.Step == "download" || choice.Step == "check"
	}
	if !validStep {
		return fmt.Errorf("the saved account prompt is invalid")
	}
	if choice.Harness != "" && choice.Harness != "codex" && choice.Harness != "claude-sdk" && choice.Harness != "opencode" {
		return fmt.Errorf("the saved account harness is invalid")
	}
	for _, value := range []string{strings.TrimPrefix(choice.Template, "opencode:"), choice.Binding, choice.Name, choice.CreatedBinding, choice.Effort} {
		if value != "" && !validIdentifier(value) {
			return fmt.Errorf("the saved account configuration name is invalid")
		}
	}
	if len(choice.Model) > 256 || strings.IndexFunc(choice.Model, unicode.IsControl) >= 0 {
		return fmt.Errorf("the saved model identifier is invalid")
	}
	return nil
}
