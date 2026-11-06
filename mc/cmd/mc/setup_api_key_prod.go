//go:build !test_fake_routing

package main

import (
	"crypto/rand"
	"fmt"
	"path/filepath"
	"strings"

	operatorconfig "mc/config"
	"mc/deployment"
	"mc/onboarding"
)

func (w *setupWizard) apiKeyAccess(harness string) error {
	if w.session.Access == nil && harness == "opencode" {
		w.session.Access = &onboarding.AccessChoice{Mode: "api_key", Step: "provider", Harness: harness}
	}
	if w.session.Access == nil {
		template := "codex-api-key"
		if harness == "claude-sdk" {
			template = "claude-api-key"
		}
		cfg, err := w.catalog()
		if err != nil {
			return err
		}
		choice := &onboarding.AccessChoice{Mode: "api_key", Step: "key", Harness: harness, Template: template, Binding: template, Model: cfg.Runtimes.Bindings[template].Model}
		identity, err := w.deps.identity(w.home, template)
		if err != nil {
			return err
		}
		if identity != "" {
			choice.Step = "key-choice"
		}
		w.session.Access = choice
	}
	return w.resumeAccess()
}

func (w *setupWizard) apiKeyStep(choice *onboarding.AccessChoice) error {
	switch choice.Step {
	case "provider":
		return w.openCodeProvider(choice)
	case "effort":
		return w.openCodeEffort(choice)
	case "download":
		if err := w.openCodeDownload(); err != nil {
			return err
		}
		choice.Step = "check"
	case "key-choice":
		answer, err := w.ui.Choose("Choose the API key", []onboarding.Choice{
			setupChoice("saved-key", "Use the saved key", "Keep the key and select a model."),
			setupChoice("replace-key", "Replace the key", "Save a new key for this connection."),
		}, 0)
		if err != nil {
			return err
		}
		choice.Step = "model"
		if answer == "replace-key" {
			choice.Step = "key"
		}
	case "key":
		key, err := w.ask(onboarding.Prompt{Label: "API key", Secret: true, Hints: []string{"Culler saves the key in the Mac Keychain."}, Check: func(value string) *onboarding.Correction {
			if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n\t ") {
				return setupCorrection("Check the API key", "", "Enter one key without spaces.")
			}
			return nil
		}})
		if err != nil {
			return err
		}
		catalog, err := deployment.AgentProfileList(w.home)
		if err != nil {
			return err
		}
		var source *deployment.AgentConnectionView
		for i := range catalog.Connections {
			if catalog.Connections[i].ID == choice.Binding {
				source = &catalog.Connections[i]
				break
			}
		}
		if source == nil {
			for _, provider := range catalog.Providers {
				if provider.ID == choice.Template {
					source = &provider
					break
				}
			}
			if source == nil {
				return fmt.Errorf("The API connection is unavailable.")
			}
		}
		save := w.deps.saveKey
		if save == nil {
			save = deployment.AgentConnectionSave
		}
		request := deployment.AgentConnectionRequest{SubmissionID: "setup-key-" + rand.Text(), Revision: catalog.Revision, ID: choice.Binding, Name: source.Name, TemplateID: choice.Template, APIKey: key, AuthorizationID: source.AuthorizationID}
		saveKey := func() error {
			_, err := save(w.home, request)
			return err
		}
		if request.AuthorizationID != "" {
			err = w.ui.Activity("Check replacement key", saveKey)
		} else {
			err = saveKey()
		}
		key = ""
		request.APIKey = ""
		if err != nil {
			return setupRuntimeFailure(err, "The host could not save the API key.")
		}
		choice.Step = "model"
		w.ui.Panel("API key saved", []string{"Choose the model that this agent uses.", "The model test verifies access and can incur provider charges."})
	case "model":
		if choice.Harness == "opencode" {
			return w.openCodeModel(choice)
		}
		model, err := w.ask(onboarding.Prompt{Label: "Model", Value: choice.Model, Hints: []string{"Enter the model identifier for this API key."}, Check: setupModelCorrection})
		if err != nil {
			return err
		}
		choice.Model = model
		_, err = operatorconfig.UpdateAtomic(filepath.Join(w.home, "config.toml"), func(cfg *operatorconfig.Config) error {
			source, ok := cfg.Runtimes.Credentials[choice.Binding]
			if !ok {
				return fmt.Errorf("The API connection is unavailable.")
			}
			cfg.Runtimes.Bindings[choice.Binding] = operatorconfig.RuntimeBinding{Name: source.Name, Harness: source.Harness(), Model: model, Credential: choice.Binding}
			return cfg.Validate()
		})
		if err != nil {
			return err
		}
		choice.Step = "check"
	}
	return nil
}
