//go:build !test_fake_routing

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mc/deployment"
	"mc/onboarding"
)

func init() {
	for _, name := range []string{"welcome", "prepare", "docker-absent", "agent-selected", "agent-review", "agent-start", "agent-resume", "agent-absent", "agent-failure", "login-failure", "override-failure", "account-added", "codex-success", "claude-success", "sign-in-retry"} {
		setupPreviewCases[name] = func(t *testing.T) setupPreviewCase { return setupPreviewInitial(t, name) }
	}
}

func setupPreviewInitial(t *testing.T, name string) setupPreviewCase {
	w, _ := setupTestWizard(t)
	result := setupPreviewCase{wizard: w}
	stage := func(index int, completed ...string) {
		w.session.Stage, w.session.Completed = index-1, completed
		w.ui.Stage(index, setupStageTitles[index-1], completed)
	}
	check := func(err error) {
		if err != nil && !errors.Is(err, onboarding.ErrExit) {
			t.Fatal(err)
		}
	}
	switch name {
	case "welcome":
		result.steps = []setupPreviewStep{{Wait: "q exit", Send: "\r", Name: "Welcome"}}
		result.run = func() { stage(1); check(w.welcome()) }
	case "prepare":
		reports := []string{"Compile Culler for Linux", "Install the agent tools"}
		for i, report := range reports {
			wait := []string{"Compiling Culler for Linux", "Installing the agent tools"}[i]
			result.steps = append(result.steps, setupPreviewStep{Wait: wait, Name: report})
		}
		w.deps.image = func(_ context.Context, _, _ string, report func(string)) error {
			for _, message := range reports {
				report(message)
				time.Sleep(1200 * time.Millisecond)
			}
			return nil
		}
		result.run = func() { stage(2, "welcome"); check(w.prepare()) }
	case "docker-absent":
		w.deps.docker = func(context.Context) onboarding.DockerStatus {
			return onboarding.DockerStatus{State: onboarding.DockerAbsent}
		}
		result.steps = []setupPreviewStep{{Wait: "q exit", Send: "q", Name: "Install Docker Desktop"}}
		result.run = func() { stage(2, "welcome"); check(w.prepare()) }
	case "agent-selected", "agent-review", "agent-start":
		cfg, err := w.catalog()
		if err != nil {
			t.Fatal(err)
		}
		binding := setupBindingForKind(cfg, setupHarnessKind("codex"))
		check(w.finishConnection(setupConnection{Binding: binding, Mode: "login"}, false, nil))
		result.steps = []setupPreviewStep{{Wait: "q exit", Send: "q", Name: "Review after agent selection"}}
		if name == "agent-review" {
			w.session.Project, w.session.WorkspaceRoot = "example-project", "/private/tmp/example-project"
			result.steps = []setupPreviewStep{{Wait: "q exit", Send: "q", Name: "Start confirmation"}}
		} else if name == "agent-start" {
			result.steps = []setupPreviewStep{}
		}
		result.run = func() {
			if name == "agent-review" {
				stage(4, setupStageIDs[:3]...)
				check(w.review())
				return
			}
			if name == "agent-start" {
				stage(5, setupStageIDs[:4]...)
				check(w.agents())
				return
			}
			stage(3, "welcome", "prepare")
			check(w.agents())
			stage(4, "welcome", "prepare", "agents")
			check(w.review())
		}
	case "agent-resume":
		cfg, err := w.catalog()
		if err != nil {
			t.Fatal(err)
		}
		check(w.finishConnection(setupConnection{Binding: setupBindingForKind(cfg, setupHarnessKind("claude-sdk")), Mode: "login"}, false, nil))
		w.session.RuntimeIncomplete = []string{setupBindingForKind(cfg, setupHarnessKind("codex"))}
		check(w.save())
		result.steps = []setupPreviewStep{{Wait: "q exit", Send: "q", Name: "Resume at the connection choices"}}
		result.run = func() { stage(3, "welcome", "prepare"); check(w.agents()) }
	case "agent-absent":
		result.steps = []setupPreviewStep{{Wait: "q exit", Send: "q", Name: "Choose an account"}}
		result.run = func() { stage(3, "welcome", "prepare"); check(w.agents()) }
	case "codex-success", "claude-success":
		selectKey := "\r"
		if name == "claude-success" {
			selectKey = "\x1b[B\r"
		}
		setupFakeSignIn(w, func(setupConnection) error { return nil })
		result.steps = []setupPreviewStep{
			{Wait: "q exit", Send: selectKey, Name: "Select the provider"},
			{Wait: "Choose the access method", Send: "\r", Name: "Select the sign-in"},
			{Wait: "q exit", Send: "\r", Name: "Confirm sign-in and model test"},
			{Wait: "q exit", Send: "q", Name: "Review the verified agent"},
		}
		result.run = func() {
			stage(3, "welcome", "prepare")
			check(w.agents())
			stage(4, "welcome", "prepare", "agents")
			check(w.review())
		}
	case "sign-in-retry":
		attempts := 0
		setupFakeSignIn(w, func(setupConnection) error {
			attempts++
			if attempts == 1 {
				return setupRuntimeFailure(&deployment.TokenRefreshError{Status: 401, Code: "refresh_token_reused"}, "")
			}
			return nil
		})
		result.steps = []setupPreviewStep{
			{Wait: "q exit", Send: "\r", Name: "Select sign-in"},
			{Wait: "q exit", Send: "\r", Name: "Confirm sign-in and model test"},
			{Wait: "q exit", Send: "\r", Name: "Retry after the connection failure"},
			{Wait: "q exit", Send: "\r", Name: "Confirm the retry"},
			{Wait: "q exit", Send: "q", Name: "Review the verified agent"},
		}
		result.run = func() {
			stage(3, "welcome", "prepare")
			check(w.agents())
			stage(4, "welcome", "prepare", "agents")
			check(w.review())
		}
	case "agent-failure":
		setupFakeSignIn(w, func(setupConnection) error {
			time.Sleep(150 * time.Millisecond)
			return setupRuntimeFailure(&deployment.TokenRefreshError{Status: 401, Code: "refresh_token_reused"}, "")
		})
		result.steps = []setupPreviewStep{
			{Wait: "q exit", Send: "\r", Name: "Select the provider"},
			{Wait: "Choose the access method", Send: "\r", Name: "Select the sign-in"},
			{Wait: "Sign in and connect?", Send: "\r", Name: "Sign in and connect"},
			{Wait: "q exit", Send: "q", Name: "Connection failure"},
		}
		result.run = func() {
			stage(3, "welcome", "prepare")
			check(w.agents())
		}
	case "login-failure", "override-failure":
		choose, cause := "\x1b[B\r", "The authorization executable is unavailable."
		if name == "override-failure" {
			choose, cause = "\r", "forbidden ambient runtime credential OPENAI_API_KEY is set; use an owner-only source file"
		}
		w.deps.identity = func(string, string) (string, error) { return "", nil }
		w.deps.connect = func(*setupWizard, setupConnection) error {
			return setupRuntimeFailure(errors.New(cause), "")
		}
		result.steps = []setupPreviewStep{
			{Wait: "q exit", Send: choose, Name: "Connect an agent"},
			{Wait: "Choose the access method", Send: "\r", Name: "Select the sign-in"},
			{Wait: "Sign in and connect?", Send: "\r", Name: "Sign in and connect"},
			{Wait: "q exit", Send: "q", Name: "Sign-in failure"},
		}
		result.run = func() {
			stage(3, "welcome", "prepare")
			check(w.agents())
		}
	case "account-added":
		w.deps.identity = func(string, string) (string, error) { return strings.Repeat("a", 64), nil }
		result.steps = []setupPreviewStep{
			{Wait: "q exit", Send: "\r", Name: "Connect an agent"},
			{Wait: "Choose the access method", Send: "\r", Name: "Select the sign-in"},
			{Wait: "Sign in and connect?", Send: "q", Name: "Added account and confirmation"},
		}
		result.run = func() {
			stage(3, "welcome", "prepare")
			check(w.agents())
		}
	}
	return result
}
