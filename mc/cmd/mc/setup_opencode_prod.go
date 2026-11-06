//go:build !test_fake_routing

package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	operatorconfig "mc/config"
	"mc/deployment"
	"mc/onboarding"
	"mc/opencodecatalog"
)

func setupProviderChoices(query string) []onboarding.Choice {
	query = strings.ToLower(strings.TrimSpace(query))
	choices := []onboarding.Choice{}
	for _, provider := range deployment.AgentProviderCatalog() {
		if query == "" && provider.Harness == "opencode" {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(provider.Name+" "+provider.CatalogEntry+" "+provider.Address), query) {
			continue
		}
		choices = append(choices, onboarding.Choice{ID: provider.ID, Label: provider.Name, Detail: provider.Address + "\n" + provider.Website, AlwaysDetail: true})
	}
	return choices
}

func (w *setupWizard) openCodeProvider(choice *onboarding.AccessChoice) error {
	for {
		query, err := w.ask(onboarding.Prompt{Label: "Provider", Hints: []string{
			"Search by name, identifier, or address.", "Culler sends keys only to providers in its catalog.",
			"Leave the search empty for the current release integrations.",
		}})
		if err != nil {
			return err
		}
		choices := setupProviderChoices(query)
		if len(choices) == 0 {
			w.ui.Panel("No provider found", []string{"Enter another name, identifier, or address."})
			continue
		}
		selected, err := w.ui.Choose("Choose the provider", choices, 0)
		if err != nil {
			return err
		}
		for _, provider := range deployment.AgentProviderCatalog() {
			if provider.ID != selected {
				continue
			}
			choice.Template, choice.Harness = selected, provider.Harness
			choice.Binding = selected
			if provider.Harness == "opencode" {
				choice.Binding = "opencode-" + provider.CatalogEntry
			}
			choice.Step, choice.Model, choice.Effort = "key", "", ""
			return nil
		}
	}
}

func (w *setupWizard) openCodeModel(choice *onboarding.AccessChoice) error {
	provider, ok := opencodecatalog.Find(strings.TrimPrefix(choice.Template, "opencode:"))
	if !ok {
		return fmt.Errorf("The release catalog lacks this provider. Select another provider.")
	}
	search := "Agents on this connection cannot search the web."
	if provider.WebSearch {
		search = "Agents on this connection can search the web."
	}
	w.ui.Panel("Configure the model", []string{search, "Prices apply to each million input and output tokens."})
	choices := []onboarding.Choice{}
	selected := 0
	for i, model := range provider.Models {
		if model.ID == choice.Model {
			selected = i
		}
		choices = append(choices, onboarding.Choice{ID: model.ID, Label: model.Name + " (" + model.ID + ")",
			Detail: fmt.Sprintf("Context: %d tokens. Input: $%g. Output: $%g.", model.Context, model.InputPrice, model.OutputPrice), AlwaysDetail: true})
	}
	model, err := w.ui.Choose("Choose the model", choices, selected)
	if err != nil {
		return err
	}
	choice.Model, choice.Effort, choice.Step = model, "", "effort"
	return nil
}

func (w *setupWizard) openCodeEffort(choice *onboarding.AccessChoice) error {
	provider, ok := opencodecatalog.Find(strings.TrimPrefix(choice.Template, "opencode:"))
	model, modelOK := provider.Model(choice.Model)
	if !ok || !modelOK {
		return fmt.Errorf("The release catalog lacks this model. Select another model.")
	}
	choices := []onboarding.Choice{setupChoice("default", "Default", "Use the provider default.")}
	for _, effort := range model.Efforts() {
		choices = append(choices, setupChoice(effort, strings.ToUpper(effort[:1])+effort[1:], ""))
	}
	effort, err := w.ui.Choose("Choose the effort", choices, 0)
	if err != nil {
		return err
	}
	if effort == "default" {
		effort = ""
	}
	if err := opencodecatalog.ValidateProfile(provider.ID, model.ID, effort); err != nil {
		return err
	}
	_, err = operatorconfig.UpdateAtomic(filepath.Join(w.home, "config.toml"), func(cfg *operatorconfig.Config) error {
		source, ok := cfg.Runtimes.Credentials[choice.Binding]
		if !ok || source.CatalogEntry != provider.ID {
			return fmt.Errorf("The API connection is unavailable.")
		}
		cfg.Runtimes.Bindings[choice.Binding] = operatorconfig.RuntimeBinding{Name: source.Name, Harness: "opencode", Model: model.ID, Effort: effort, Credential: choice.Binding}
		return cfg.Validate()
	})
	if err == nil {
		choice.Effort, choice.Step = effort, "download"
	}
	return err
}

func (w *setupWizard) openCodeDownload() error {
	status, start := w.deps.openCodeStatus, w.deps.openCodeStart
	if status == nil {
		status = deployment.OpenCodeDownloadStatus
	}
	if start == nil {
		start = deployment.StartOpenCodeDownload
	}
	retry := false
	return w.progressOperation("Prepare OpenCode", func(report func(string)) error {
		current := status(w.home)
		if current == nil || retry {
			operation, err := start(w.home, current != nil)
			if err != nil {
				return err
			}
			current = &operation
		}
		retry = true
		return waitOpenCodeDownload(current, func() *deployment.AgentProfileOperation { return status(w.home) }, report, time.Now().Add(20*time.Minute))
	})
}

func waitOpenCodeDownload(current *deployment.AgentProfileOperation, status func() *deployment.AgentProfileOperation, report func(string), deadline time.Time) error {
	for {
		if current == nil {
			return &setupFailure{cause: "The OpenCode download status is unavailable. Retry the download."}
		}
		report(current.Message)
		switch current.Status {
		case "succeeded":
			return nil
		case "failed":
			return &setupFailure{cause: current.Message}
		}
		if time.Now().After(deadline) {
			return &setupFailure{cause: "The OpenCode download exceeded its time limit. Retry the download."}
		}
		time.Sleep(500 * time.Millisecond)
		current = status()
	}
}
