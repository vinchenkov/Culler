package opencodecatalog

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// The OpenCode release check proves that the pinned OpenCode derives testdata/live-facts.json
// from testdata/live-record.json, and that each record in testdata/live-models.json gives the same facts.
func liveFixture(t *testing.T) (LiveRecord, []ProviderFacts, []ProviderFacts) {
	t.Helper()
	list, err := os.ReadFile("testdata/live-record.json")
	if err != nil {
		t.Fatal(err)
	}
	record, err := ReadLiveRecord(list)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile("testdata/live-facts.json")
	if err != nil {
		t.Fatal(err)
	}
	var reports struct{ Baseline, Live json.RawMessage }
	if err := json.Unmarshal(body, &reports); err != nil {
		t.Fatal(err)
	}
	baseline, err := ParseFacts(reports.Baseline)
	if err != nil {
		t.Fatal(err)
	}
	live, err := ParseFacts(reports.Live)
	if err != nil {
		t.Fatal(err)
	}
	return record, baseline, live
}

func liveFact(t *testing.T, live []ProviderFacts, id string) ModelFact {
	t.Helper()
	for _, provider := range live {
		for _, fact := range provider.Models {
			if provider.ID == "moonshotai" && fact.ID == id {
				return fact
			}
		}
	}
	t.Fatalf("The fixture facts lack %s.", id)
	return ModelFact{}
}

func modelIDs(models []Model) []string {
	ids := []string{}
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func TestLiveModelsOfTheFixtureRecord(t *testing.T) {
	record, baseline, live := liveFixture(t)
	models := LiveModels(record, baseline, live)
	if len(models) != 1 {
		t.Fatalf("The live model list added a provider: %v", models)
	}
	// kimi-k2.6 equals the model record of the pinned OpenCode, so it stays a release catalog model.
	want := []string{"kimi-k3", "kimi-fixture-pair-fast", "kimi-fixture-pair", "kimi-fixture-new", "kimi-fixture-new-fast", "kimi-fixture-path"}
	if got := modelIDs(models["moonshotai"]); !slices.Equal(got, want) {
		t.Fatalf("The live models are %q.", got)
	}
	found := map[string]Model{}
	for _, model := range models["moonshotai"] {
		found[model.ID] = model
		var entries map[string]struct {
			ID     string                     `json:"id"`
			Name   string                     `json:"name"`
			Env    []string                   `json:"env"`
			Doc    string                     `json:"doc"`
			Models map[string]json.RawMessage `json:"models"`
		}
		if err := json.Unmarshal([]byte(model.Record), &entries); err != nil || len(entries) != 1 || entries["moonshotai"].Name != "Moonshot AI" ||
			!slices.Equal(entries["moonshotai"].Env, []string{"MOONSHOT_API_KEY"}) || len(entries["moonshotai"].Models) == 0 || len(entries["moonshotai"].Models) > 2 {
			t.Fatalf("The model record of %s is invalid: %v", model.ID, err)
		}
		if model.KeyPlacement != "bearer" || model.ModelLocation != "body" || model.Package != "@opencode/ai/providers/moonshot/chat" || !ValidLiveModel(mustFind(t, "moonshotai"), model) {
			t.Fatalf("The live model %s lost its recorded request.", model.ID)
		}
	}
	changed := found["kimi-k3"]
	if changed.Context != 2097152 || changed.InputPrice != 2.5 || changed.OutputPrice != 15 || changed.Address != "https://api.moonshot.ai/v1" || !strings.Contains(changed.Record, `"output":131072`) {
		t.Fatalf("The live model lost the facts of the live model list: %+v", changed)
	}
	added := found["kimi-fixture-new"]
	if added.Name != "Kimi Fixture New" || added.Context != 524288 || added.InputPrice != 1.25 || added.OutputPrice != 6.5 || !slices.Equal(added.Efforts(), []string{"low", "high", "max"}) {
		t.Fatalf("The new live model has other facts: %+v", added)
	}
	if found["kimi-fixture-path"].Address != "https://api.moonshot.ai/v2" {
		t.Fatal("The live model lost the path of its address.")
	}
	// A mode of a model has the record entry of that model.
	if mode := found["kimi-fixture-new-fast"]; !strings.Contains(mode.Record, `"kimi-fixture-new":`) || strings.Contains(mode.Record, `"kimi-fixture-new-fast":`) {
		t.Fatal("The mode lost the record entry of its model.")
	}
	// The last entry gives the model, so the record keeps the order of the live model list.
	pair := found["kimi-fixture-pair-fast"].Record
	if first, last := strings.Index(pair, `"kimi-fixture-pair-fast":`), strings.Index(pair, `"kimi-fixture-pair":`); first < 0 || last < first || found["kimi-fixture-pair-fast"].Name != "Kimi Fixture Pair Fast" {
		t.Fatal("The model record changed the order of its entries.")
	}
	golden, _ := json.MarshalIndent(models, "", " ")
	golden = append(golden, '\n')
	if os.Getenv("UPDATE_LIVE_FIXTURES") != "" {
		if err := os.WriteFile("testdata/live-models.json", golden, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if recorded, err := os.ReadFile("testdata/live-models.json"); err != nil || !bytes.Equal(recorded, golden) {
		t.Fatalf("The live models differ from testdata/live-models.json. Set UPDATE_LIVE_FIXTURES=1, then run the OpenCode release check: %v", err)
	}
}

func mustFind(t *testing.T, id string) Provider {
	t.Helper()
	provider, ok := Find(id)
	if !ok {
		t.Fatalf("The release catalog lacks %s.", id)
	}
	return provider
}

func TestLiveModelConditions(t *testing.T) {
	record, _, live := liveFixture(t)
	provider := mustFind(t, "moonshotai")
	valid := liveFact(t, live, "kimi-fixture-new")
	accepted := map[string]struct {
		change  func(*ModelFact)
		address string
	}{
		"fixture fact":            {func(*ModelFact) {}, "https://api.moonshot.ai/v1"},
		"other path":              {func(f *ModelFact) { f.Address = "https://api.moonshot.ai/anthropic/v1" }, "https://api.moonshot.ai/anthropic/v1"},
		"host in capital letters": {func(f *ModelFact) { f.Address = "https://API.Moonshot.AI/v1" }, "https://api.moonshot.ai/v1"},
		"default port":            {func(f *ModelFact) { f.Address = "https://api.moonshot.ai:443/v2/" }, "https://api.moonshot.ai/v2/"},
		"beta status":             {func(f *ModelFact) { f.Status = "beta" }, "https://api.moonshot.ai/v1"},
	}
	for name, test := range accepted {
		fact := valid
		test.change(&fact)
		model, ok := liveModel(provider, record, fact)
		if !ok || model.Address != test.address || model.Record == "" {
			t.Fatalf("The conditions refused a correct model (%s): %+v", name, model)
		}
	}
	refused := map[string]func(*ModelFact){
		"model of another type":        func(f *ModelFact) { f.Type = "embedding" },
		"model without tool support":   func(f *ModelFact) { f.Tools = false },
		"deprecated model":             func(f *ModelFact) { f.Status = "deprecated" },
		"address without HTTPS":        func(f *ModelFact) { f.Address = "http://api.moonshot.ai/v1" },
		"address with a credential":    func(f *ModelFact) { f.Address = "https://key:value@api.moonshot.ai/v1" },
		"address with a user":          func(f *ModelFact) { f.Address = "https://api.moonshot.ai@live-model.invalid/v1" },
		"address with a query":         func(f *ModelFact) { f.Address = "https://api.moonshot.ai/v1?version=2" },
		"address with a fragment":      func(f *ModelFact) { f.Address = "https://api.moonshot.ai/v1#part" },
		"address with a placeholder":   func(f *ModelFact) { f.Address = "https://api.moonshot.ai/{region}/v1" },
		"address with an escape":       func(f *ModelFact) { f.Address = "https://api.moonshot.ai/%2e%2e/v1" },
		"address with a parent path":   func(f *ModelFact) { f.Address = "https://api.moonshot.ai/v1/../v2" },
		"address with a backslash":     func(f *ModelFact) { f.Address = `https://live-model.invalid\@api.moonshot.ai/v1` },
		"address with a space":         func(f *ModelFact) { f.Address = "https://api.moonshot.ai/v1 " },
		"address without a host":       func(f *ModelFact) { f.Address = "https:///v1" },
		"empty address":                func(f *ModelFact) { f.Address = "" },
		"other origin":                 func(f *ModelFact) { f.Address = "https://live-model.invalid/v1" },
		"other port":                   func(f *ModelFact) { f.Address = "https://api.moonshot.ai:8443/v1" },
		"host below another domain":    func(f *ModelFact) { f.Address = "https://api.moonshot.ai.live-model.invalid/v1" },
		"host with a prefix":           func(f *ModelFact) { f.Address = "https://live.api.moonshot.ai/v1" },
		"package without a record":     func(f *ModelFact) { f.Package = "@opencode/ai/providers/moonshot/messages" },
		"package of another source":    func(f *ModelFact) { f.Package = "external-package" },
		"model without a record entry": func(f *ModelFact) { f.Sources = []string{"absent"} },
		"model without sources":        func(f *ModelFact) { f.Sources = nil },
		"model without an identifier":  func(f *ModelFact) { f.ID = "" },
		"negative context":             func(f *ModelFact) { f.Context = -1 },
		"negative price":               func(f *ModelFact) { f.InputPrice = -1 },
	}
	for name, change := range refused {
		fact := valid
		change(&fact)
		if model, ok := liveModel(provider, record, fact); ok {
			t.Fatalf("The conditions accepted an incorrect model (%s): %+v", name, model)
		}
	}
	// The conditions require a provider of the release catalog.
	outside := []ProviderFacts{{ID: "fixture-provider", Models: []ModelFact{valid}}}
	if models := LiveModels(record, nil, outside); len(models) != 0 {
		t.Fatalf("The live model list added a provider: %v", models)
	}
	if _, _, ok := RecordedPackage("@opencode/ai/providers/moonshot/chat"); !ok {
		t.Fatal("The release catalog lost a recorded package.")
	}
}

func TestLiveModelReplacesTheReleaseCatalogModel(t *testing.T) {
	record, baseline, live := liveFixture(t)
	released := mustFind(t, "moonshotai")
	provider := released.WithLive(LiveModels(record, baseline, live)["moonshotai"])
	ids := modelIDs(provider.Models)
	if len(ids) != len(released.Models)+5 || !slices.Equal(ids[:5], []string{"kimi-fixture-pair-fast", "kimi-fixture-pair", "kimi-fixture-new", "kimi-fixture-new-fast", "kimi-fixture-path"}) ||
		!slices.Equal(ids[5:], modelIDs(released.Models)) {
		t.Fatalf("The model list is %q.", ids)
	}
	model, _ := provider.Model("kimi-k3")
	before, _ := released.Model("kimi-k3")
	if model.Context != 2097152 || model.Record == "" || before.Context != 1048576 || before.Record != "" {
		t.Fatal("The live model did not replace the release catalog model.")
	}
	if unchanged, _ := provider.Model("kimi-k2.6"); unchanged.Record != "" {
		t.Fatal("An equal model received a model record.")
	}
	if same := released.WithLive(nil); len(same.Models) != len(released.Models) {
		t.Fatal("The release catalog models changed.")
	}
}

func TestLiveRecordKeepsOnlyReleaseCatalogProviders(t *testing.T) {
	var list map[string]map[string]json.RawMessage
	body, _ := os.ReadFile("testdata/live-record.json")
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	list["moonshotai"]["name"] = json.RawMessage(`"Another Name"`)
	list["moonshotai"]["env"] = json.RawMessage(`["ANOTHER_KEY", "SECOND_KEY"]`)
	list["moonshotai"]["doc"] = json.RawMessage(`"https://live-model.invalid/docs"`)
	changed, _ := json.Marshal(list)
	record, err := ReadLiveRecord(changed)
	if err != nil {
		t.Fatal(err)
	}
	var derived map[string]struct {
		Name string   `json:"name"`
		Env  []string `json:"env"`
		Doc  string   `json:"doc"`
	}
	provider := mustFind(t, "moonshotai")
	if err := json.Unmarshal(record.Body, &derived); err != nil || len(derived) != 1 || derived["moonshotai"].Name != provider.Name ||
		!slices.Equal(derived["moonshotai"].Env, []string{provider.KeyVariable}) || derived["moonshotai"].Doc != provider.Website {
		t.Fatalf("The live model list changed a provider: %s", record.Body)
	}
	for _, invalid := range []string{"", "[]", `{"fixture-provider":{"models":{"model":{}}}}`, `{"moonshotai":{"models":{}}}`} {
		if _, err := ReadLiveRecord([]byte(invalid)); err == nil {
			t.Fatalf("The invalid live model list passed: %q", invalid)
		}
	}
}

func TestRetainedLiveModelMustMeetTheConditions(t *testing.T) {
	record, baseline, live := liveFixture(t)
	provider := mustFind(t, "moonshotai")
	valid := LiveModels(record, baseline, live)["moonshotai"][0]
	for name, change := range map[string]func(*Model){
		"other origin":         func(m *Model) { m.Address = "https://live-model.invalid/v1" },
		"other key placement":  func(m *Model) { m.KeyPlacement = "x-api-key" },
		"other model location": func(m *Model) { m.ModelLocation = "path" },
		"other package":        func(m *Model) { m.Package = "@opencode/ai/providers/anthropic" },
		"no model record":      func(m *Model) { m.Record = "" },
		"invalid model record": func(m *Model) { m.Record = "{" },
		"large model record":   func(m *Model) { m.Record = `{"a":"` + strings.Repeat("a", MaxModelRecordBytes) + `"}` },
	} {
		model := valid
		change(&model)
		if ValidLiveModel(provider, model) {
			t.Fatalf("The conditions accepted a changed model (%s).", name)
		}
	}
	other := mustFind(t, "moonshotai-cn")
	if ValidLiveModel(other, valid) {
		t.Fatal("A provider accepted the address of another provider.")
	}
}
