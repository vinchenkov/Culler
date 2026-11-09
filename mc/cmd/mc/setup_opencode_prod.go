//go:build !test_fake_routing

package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	operatorconfig "mc/config"
	"mc/deployment"
	"mc/onboarding"
	"mc/opencodecatalog"
)

// setupProviderSearch matches the provider search of the dashboard.
var setupProviderSearch = onboarding.Search{
	Label:      "Provider",
	Hints:      []string{"Search by name, identifier, or address.", "Culler sends keys only to providers in its catalog."},
	QueryHints: []string{"Leave the search empty for the current release integrations."},
	Initial:    "Current release integrations.",
	Find:       setupProviderChoices,
	Empty:      onboarding.Correction{Title: "No provider found", Lines: []string{"Enter another name, identifier, or address."}},
	Choose:     "Choose the provider",
}

// setupProviderChoices lists name matches before identifier matches and address matches.
func setupProviderChoices(query string) []onboarding.Choice {
	query = strings.ToLower(strings.TrimSpace(query))
	type match struct {
		rank   int
		choice onboarding.Choice
	}
	matches := []match{}
	for _, provider := range deployment.AgentProviderCatalog() {
		rank := setupProviderRank(provider, query)
		if rank < 0 || query == "" && provider.Harness == "opencode" {
			continue
		}
		matches = append(matches, match{rank, onboarding.Choice{ID: provider.ID, Label: provider.Name, Detail: provider.Address + "\n" + provider.Website, AlwaysDetail: true}})
	}
	slices.SortStableFunc(matches, func(a, b match) int { return a.rank - b.rank })
	choices := []onboarding.Choice{}
	for _, match := range matches {
		choices = append(choices, match.choice)
	}
	return choices
}

// setupProviderRank returns -1 for a provider that does not match the query.
// Each catalog identifier starts with "opencode:", so the search ignores that prefix.
func setupProviderRank(provider deployment.AgentConnectionView, query string) int {
	name := strings.ToLower(provider.Name)
	words := strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	switch {
	case strings.HasPrefix(name, query):
		return 0
	case slices.ContainsFunc(words, func(word string) bool { return strings.HasPrefix(word, query) }):
		return 1
	case strings.Contains(name, query):
		return 2
	case strings.Contains(strings.ToLower(provider.CatalogEntry), query) || strings.Contains(strings.ToLower(strings.TrimPrefix(provider.ID, "opencode:")), query):
		return 3
	case strings.Contains(strings.ToLower(provider.Address), query):
		return 4
	}
	return -1
}

func (w *setupWizard) openCodeProvider(choice *onboarding.AccessChoice) error {
	selected, err := w.search(setupProviderSearch)
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
	return fmt.Errorf("The release catalog lacks this provider. Select another provider.")
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
