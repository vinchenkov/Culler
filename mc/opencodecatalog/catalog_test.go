package opencodecatalog

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestCatalogMatchesRecordedRequests(t *testing.T) {
	catalog := Release()
	if len(catalog.Providers) < 190 {
		t.Fatal("The release lacks supported providers.")
	}
	var requests map[string]struct {
		KeyPlacement  string `json:"key_placement"`
		ModelLocation string `json:"model_location"`
		Request       struct {
			Path      string `json:"path"`
			KeyHeader string `json:"key_header"`
			Model     string `json:"model"`
		} `json:"request"`
	}
	if err := json.Unmarshal(catalog.Requests, &requests); err != nil {
		t.Fatal(err)
	}
	for _, provider := range catalog.Providers {
		for _, model := range provider.Models {
			request, ok := requests[model.Package]
			if !ok || request.KeyPlacement != model.KeyPlacement || request.ModelLocation != model.ModelLocation || request.Request.Path == "" {
				t.Fatalf("The model lacks request evidence: %s/%s", provider.ID, model.ID)
			}
			header := model.KeyPlacement
			if header == "bearer" {
				header = "authorization"
			}
			if request.Request.KeyHeader != header {
				t.Fatal("The key header differs from its request.")
			}
			if model.ModelLocation == "body" && request.Request.Model == "" {
				t.Fatal("The request lacks its body model.")
			}
			for _, effort := range model.Efforts() {
				if !slices.Contains(model.Variants, effort) {
					t.Fatal("The effort lacks a native variant.")
				}
			}
		}
	}
}

func TestCatalogRefusesInvalidAddressesAndModels(t *testing.T) {
	for _, change := range []func(*Model){
		func(m *Model) { m.Address = "http://provider.invalid/v1" }, func(m *Model) { m.Address = "https://{account}.invalid" },
		func(m *Model) { m.KeyPlacement = "api-key" }, func(m *Model) { m.ModelLocation = "query" }, func(m *Model) { m.Package = "external-package" },
	} {
		catalog, _ := Parse(body)
		change(&catalog.Providers[0].Models[0])
		raw, _ := json.Marshal(catalog)
		if _, err := Parse(raw); err == nil {
			t.Fatal("The invalid model entered the catalog.")
		}
	}
}

func TestCatalogEffortsAndRefusedKeySiblings(t *testing.T) {
	provider, ok := Find("moonshotai")
	if !ok {
		t.Fatal("The acceptance provider is absent.")
	}
	model, ok := provider.Model("kimi-k3")
	if !ok || !slices.Equal(model.Efforts(), []string{"low", "high", "max"}) {
		t.Fatal("The native variants differ.")
	}
	for _, effort := range []string{"", "low", "high", "max"} {
		if err := ValidateProfile(provider.ID, model.ID, effort); err != nil {
			t.Fatal(err)
		}
	}
	if ValidateProfile(provider.ID, model.ID, "thinking") == nil || ValidateProfile(provider.ID, "absent", "") == nil || ValidateProfile("absent", model.ID, "") == nil {
		t.Fatal("An invalid profile passed.")
	}
	for _, other := range Release().Providers {
		if other.ID != provider.ID && other.KeyVariable == provider.KeyVariable && !strings.Contains(provider.RefusedKey(), other.Name) {
			t.Fatal("The refusal omitted a sibling provider.")
		}
	}
}
