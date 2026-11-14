// Package opencodecatalog reads the provider catalog of the release.
package opencodecatalog

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
)

//go:embed catalog.json
var body []byte

type Model struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Context       int      `json:"context"`
	InputPrice    float64  `json:"input_price"`
	OutputPrice   float64  `json:"output_price"`
	Address       string   `json:"address"`
	Package       string   `json:"package"`
	KeyPlacement  string   `json:"key_placement"`
	ModelLocation string   `json:"model_location"`
	Variants      []string `json:"variants"`
	// Record is the model record that OpenCode reads for a live model.
	Record string `json:"record,omitempty"`
}

type Provider struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Website     string  `json:"website"`
	KeyVariable string  `json:"key_variable"`
	WebSearch   bool    `json:"web_search"`
	Models      []Model `json:"models"`
}

type Catalog struct {
	Version          int             `json:"version"`
	OpenCodeVersion  string          `json:"opencode_version"`
	WebSearchVersion string          `json:"websearch_version"`
	Providers        []Provider      `json:"providers"`
	Requests         json.RawMessage `json:"requests"`
}

func Parse(data []byte) (Catalog, error) {
	var catalog Catalog
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return catalog, err
	}
	if catalog.Version != 1 || catalog.OpenCodeVersion != "2.2.7" || catalog.WebSearchVersion != "0.7.1" || len(catalog.Providers) == 0 {
		return Catalog{}, fmt.Errorf("The provider catalog version is invalid.")
	}
	ids := map[string]bool{}
	for _, provider := range catalog.Providers {
		if provider.ID == "" || ids[provider.ID] || provider.Name == "" || provider.KeyVariable == "" || len(provider.Models) == 0 {
			return Catalog{}, fmt.Errorf("The provider catalog entry is invalid.")
		}
		ids[provider.ID] = true
		models := map[string]bool{}
		for _, model := range provider.Models {
			address, err := url.Parse(model.Address)
			if err != nil || address.Scheme != "https" || address.Hostname() == "" || address.User != nil || address.RawQuery != "" || address.Fragment != "" || strings.ContainsAny(model.Address, "{}$<>") ||
				model.ID == "" || models[model.ID] || model.Name == "" || model.Context < 0 || model.InputPrice < 0 || model.OutputPrice < 0 || model.Record != "" ||
				!strings.HasPrefix(model.Package, "@opencode/ai/providers/") ||
				!slices.Contains([]string{"bearer", "x-api-key", "x-goog-api-key"}, model.KeyPlacement) ||
				!slices.Contains([]string{"body", "path"}, model.ModelLocation) {
				return Catalog{}, fmt.Errorf("The provider catalog model is invalid.")
			}
			models[model.ID] = true
		}
	}
	return catalog, nil
}

var release = sync.OnceValue(func() Catalog {
	catalog, err := Parse(body)
	if err != nil {
		panic(err)
	}
	return catalog
})

func Release() Catalog { return release() }

func Find(id string) (Provider, bool) {
	for _, provider := range Release().Providers {
		if provider.ID == id {
			return provider, true
		}
	}
	return Provider{}, false
}

func (provider Provider) Model(id string) (Model, bool) {
	for _, model := range provider.Models {
		if model.ID == id {
			return model, true
		}
	}
	return Model{}, false
}

func (model Model) Efforts() []string {
	levels := []string{}
	for _, level := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "thinking"} {
		if slices.Contains(model.Variants, level) {
			levels = append(levels, level)
		}
	}
	return levels
}

// ValidateModel checks a model and an effort level against the models of the provider.
func (provider Provider) ValidateModel(modelID, effort string) error {
	model, ok := provider.Model(modelID)
	if !ok {
		return fmt.Errorf("The connection does not offer this model. Select another model.")
	}
	if effort != "" && !slices.Contains(model.Efforts(), effort) {
		return fmt.Errorf("This model does not offer the selected effort. Select another effort level.")
	}
	return nil
}

func (provider Provider) RefusedKey() string {
	names := []string{}
	for _, other := range Release().Providers {
		if other.ID != provider.ID && other.KeyVariable == provider.KeyVariable {
			names = append(names, other.Name)
		}
	}
	message := provider.Name + " refused the key."
	if len(names) != 0 {
		message += " These providers accept the same key type at other addresses: " + strings.Join(names, ", ") + "."
	}
	return message + " Check the site where you created the key."
}
