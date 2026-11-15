package deployment

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	operatorconfig "mc/config"
	"mc/opencodecatalog"
)

// The fixture facts are the facts that the pinned OpenCode derives from the fixture model record.
// The OpenCode release check proves that relation.
type openCodeLiveFixture struct {
	list     []byte
	baseline []byte
	live     []byte
}

func readOpenCodeLiveFixture(t *testing.T) openCodeLiveFixture {
	t.Helper()
	list, err := os.ReadFile(filepath.Join("..", "opencodecatalog", "testdata", "live-record.json"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join("..", "opencodecatalog", "testdata", "live-facts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reports struct{ Baseline, Live json.RawMessage }
	if err := json.Unmarshal(body, &reports); err != nil {
		t.Fatal(err)
	}
	return openCodeLiveFixture{list: list, baseline: reports.Baseline, live: reports.Live}
}

// without removes models from the live model list and from the facts that OpenCode derives from it.
func (fixture openCodeLiveFixture) without(t *testing.T, models ...string) openCodeLiveFixture {
	t.Helper()
	var list map[string]map[string]json.RawMessage
	if err := json.Unmarshal(fixture.list, &list); err != nil {
		t.Fatal(err)
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(list["moonshotai"]["models"], &entries); err != nil {
		t.Fatal(err)
	}
	for _, model := range models {
		delete(entries, model)
	}
	list["moonshotai"]["models"], _ = json.Marshal(entries)
	var live []map[string]json.RawMessage
	if err := json.Unmarshal(fixture.live, &live); err != nil {
		t.Fatal(err)
	}
	for _, provider := range live {
		var facts []map[string]any
		if err := json.Unmarshal(provider["models"], &facts); err != nil {
			t.Fatal(err)
		}
		facts = slices.DeleteFunc(facts, func(fact map[string]any) bool { return slices.Contains(models, fact["id"].(string)) })
		provider["models"], _ = json.Marshal(facts)
	}
	out := openCodeLiveFixture{baseline: fixture.baseline}
	out.list, _ = json.Marshal(list)
	out.live, _ = json.Marshal(live)
	return out
}

func (fixture openCodeLiveFixture) dependencies(t *testing.T) openCodeModelRefreshDependencies {
	t.Helper()
	return openCodeModelRefreshDependencies{
		Fetch: func(context.Context) ([]byte, error) { return fixture.list, nil },
		Derive: func(_ context.Context, record []byte) ([]byte, []byte, error) {
			var providers map[string]json.RawMessage
			if err := json.Unmarshal(record, &providers); err != nil || len(providers) != 1 || providers["moonshotai"] == nil {
				t.Errorf("OpenCode received a provider outside the release catalog: %v", err)
			}
			return fixture.baseline, fixture.live, nil
		},
	}
}

// openCodeLiveHome returns a deployment with one OpenCode connection and no refresh process.
func openCodeLiveHome(t *testing.T) (string, *int) {
	t.Helper()
	home := agentProfileTestHome(t)
	_, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), func(cfg *operatorconfig.Config) error {
		cfg.Runtimes.Credentials["catalog"] = operatorconfig.CredentialSource{Name: "Catalog", Kind: operatorconfig.CredentialKindOpenCodeAPIKey, CatalogEntry: "moonshotai"}
		return cfg.Validate()
	})
	if err != nil {
		t.Fatal(err)
	}
	launches := 0
	previous := openCodeModelRefreshLaunch
	openCodeModelRefreshLaunch = func(string, string) error { launches++; return nil }
	t.Cleanup(func() { openCodeModelRefreshLaunch = previous; forgetOpenCodeLiveModels() })
	forgetOpenCodeLiveModels()
	return home, &launches
}

// forgetOpenCodeLiveModels gives the next read the state of a new process.
func forgetOpenCodeLiveModels() {
	openCodeLiveCache.Lock()
	openCodeLiveCache.info, openCodeLiveCache.models = nil, nil
	openCodeLiveCache.Unlock()
}

func installOpenCodeFixture(t *testing.T, home string) {
	t.Helper()
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	record := agentProfileOperationRecord{AgentProfileOperation: AgentProfileOperation{OperationID: openCodeOperation, Kind: "opencode_download",
		Status: "succeeded", Message: "OpenCode is ready.", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	if err := writeAgentProfileOperation(root, record); err != nil {
		t.Fatal(err)
	}
}

func refreshOpenCodeFixture(t *testing.T, home string, deps openCodeModelRefreshDependencies) error {
	t.Helper()
	if err := startOpenCodeModelRefresh(home, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	return runOpenCodeModelRefreshWith(home, deps)
}

func openCodeModelIDs(t *testing.T, home string) (AgentModelList, []string) {
	t.Helper()
	list, err := AgentConnectionModels(home, AgentModelListRequest{ConnectionID: "catalog"})
	if err != nil {
		t.Fatalf("The model list request failed: %v", err)
	}
	ids := []string{}
	for _, model := range list.Models {
		ids = append(ids, model.ID)
	}
	return list, ids
}

func releaseModelIDs(t *testing.T) []string {
	t.Helper()
	provider, ok := opencodecatalog.Find("moonshotai")
	if !ok {
		t.Fatal("The release catalog lacks the fixture provider.")
	}
	ids := []string{}
	for _, model := range provider.Models {
		ids = append(ids, model.ID)
	}
	return ids
}

func TestOpenCodeModelListHoldsLiveModelsOfTheLatestRefresh(t *testing.T) {
	home, launches := openCodeLiveHome(t)
	fixture := readOpenCodeLiveFixture(t)
	previous := agentModelAccess
	defer func() { agentModelAccess = previous }()
	agentModelAccess = func(string, string) (modelCatalogAccess, error) {
		t.Fatal("The model list requested provider access.")
		return modelCatalogAccess{}, nil
	}
	list, ids := openCodeModelIDs(t, home)
	if list.Source != OpenCodeModelSourceCatalog || list.Refreshing || !list.WebSearch || !slices.Equal(ids, releaseModelIDs(t)) || *launches != 0 {
		t.Fatalf("The model list holds other models than the release catalog: %q", ids)
	}
	installOpenCodeFixture(t, home)
	list, ids = openCodeModelIDs(t, home)
	if *launches != 1 || !list.Refreshing || list.Source != OpenCodeModelSourceCatalog || !slices.Equal(ids, releaseModelIDs(t)) {
		t.Fatalf("The model list request did not start one refresh and return the available models: %d %+v", *launches, list)
	}
	if err := runOpenCodeModelRefreshWith(home, fixture.dependencies(t)); err != nil {
		t.Fatal(err)
	}
	added := []string{"kimi-fixture-pair-fast", "kimi-fixture-pair", "kimi-fixture-new", "kimi-fixture-new-fast", "kimi-fixture-path"}
	check := func(state string) {
		t.Helper()
		list, ids := openCodeModelIDs(t, home)
		if list.Source != OpenCodeModelSourceLive || list.Refreshing || !slices.Equal(ids, append(slices.Clone(added), releaseModelIDs(t)...)) {
			t.Fatalf("%s: the model list is %q from %s.", state, ids, list.Source)
		}
		for _, model := range list.Models {
			switch model.ID {
			case "kimi-k3":
				// The live model replaces the release catalog model with the same identifier.
				if model.Context != 2097152 || model.InputPrice != 2.5 || model.Name != "Kimi K3" {
					t.Fatalf("%s: the release catalog model stayed: %+v", state, model)
				}
			case "kimi-fixture-new":
				if model.Name != "Kimi Fixture New" || model.Context != 524288 || model.InputPrice != 1.25 || model.OutputPrice != 6.5 ||
					!slices.Equal(model.EffortLevels, []string{"low", "high", "max"}) || !model.EffortSupportKnown {
					t.Fatalf("%s: the live model shows other facts: %+v", state, model)
				}
			}
		}
		for _, effort := range []string{"", "low", "max"} {
			if err := ValidateOpenCodeProfile(home, "moonshotai", "kimi-fixture-new", effort); err != nil {
				t.Fatalf("%s: %v", state, err)
			}
		}
		if err := ValidateOpenCodeProfile(home, "moonshotai", "kimi-fixture-new", "thinking"); err == nil {
			t.Fatalf("%s: the live model accepted an effort that OpenCode does not derive.", state)
		}
	}
	check("after the refresh")
	if status, err := GetAgentProfileOperation(home, openCodeModelsOperation); err != nil || status.Status != "succeeded" {
		t.Fatalf("The refresh did not record its success: %+v %v", status, err)
	}
	forgetOpenCodeLiveModels()
	check("after a restart")
	if *launches != 1 {
		t.Fatal("A model list request started a second refresh during the hour.")
	}

	identity := openCodeModelIdentity
	defer func() { openCodeModelIdentity = identity }()
	for name, changed := range map[string]func() (string, string){
		"release catalog": func() (string, string) { _, version := identity(); return strings.Repeat("0", 64), version },
		"pinned OpenCode": func() (string, string) { catalog, _ := identity(); return catalog, "0.0.1" },
	} {
		openCodeModelIdentity = changed
		list, ids := openCodeModelIDs(t, home)
		if list.Source != OpenCodeModelSourceCatalog || !slices.Equal(ids, releaseModelIDs(t)) {
			t.Fatalf("The live models stayed after a change of the %s: %q", name, ids)
		}
		if err := ValidateOpenCodeProfile(home, "moonshotai", "kimi-fixture-new", ""); err == nil || err.Error() != "The connection does not offer this model. Select another model." {
			t.Fatalf("The refusal changed after a change of the %s: %v", name, err)
		}
	}
	openCodeModelIdentity = identity
	check("with the release that derived the live models")
}

func TestOpenCodeModelRefreshStartsOncePerHourAfterTheInstallation(t *testing.T) {
	home, launches := openCodeLiveHome(t)
	now := time.Now()
	for _, force := range []bool{false, true} {
		if err := startOpenCodeModelRefresh(home, force, now); err != nil || *launches != 0 {
			t.Fatalf("A refresh started without the installed OpenCode: %v", err)
		}
	}
	installOpenCodeFixture(t, home)
	finish := func() {
		t.Helper()
		if _, err := updateAgentProfileOperation(home, openCodeModelsOperation, func(record *agentProfileOperationRecord) error {
			record.Status = "succeeded"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for step, test := range []struct {
		force  bool
		after  time.Duration
		finish bool
		want   int
	}{
		{false, 0, false, 1},                // The first request starts a refresh.
		{false, time.Minute, false, 1},      // A refresh waits or runs.
		{true, time.Minute, true, 1},        // The installation does not start a second refresh beside it.
		{false, 59 * time.Minute, false, 1}, // A refresh started during the previous hour.
		{true, 2 * time.Minute, false, 2},   // The installation starts a refresh after the previous one.
		{false, 30 * time.Minute, true, 2},
		{false, 63 * time.Minute, false, 3}, // The hour ended.
	} {
		if err := startOpenCodeModelRefresh(home, test.force, now.Add(test.after)); err != nil || *launches != test.want {
			t.Fatalf("Step %d started %d refreshes: %v", step, *launches, err)
		}
		if test.finish {
			finish()
		}
	}
	// A refresh process that stopped does not prevent the refresh after an installation.
	if err := startOpenCodeModelRefresh(home, true, now.Add(63*time.Minute+openCodeModelRefreshLimit)); err != nil || *launches != 4 {
		t.Fatalf("An interrupted refresh prevented the next refresh: %d %v", *launches, err)
	}
	finish()
	openCodeModelRefreshLaunch = func(string, string) error { return errors.New("no process") }
	if err := startOpenCodeModelRefresh(home, true, now.Add(3*time.Hour)); err == nil {
		t.Fatal("The start failure had no cause.")
	}
	list, ids := openCodeModelIDs(t, home)
	if list.Refreshing || !slices.Equal(ids, releaseModelIDs(t)) {
		t.Fatal("A refresh that did not start changed the model list.")
	}
}

func TestFailedOpenCodeModelRefreshPreservesRetainedLiveModels(t *testing.T) {
	home, _ := openCodeLiveHome(t)
	fixture := readOpenCodeLiveFixture(t)
	installOpenCodeFixture(t, home)
	if err := refreshOpenCodeFixture(t, home, fixture.dependencies(t)); err != nil {
		t.Fatal(err)
	}
	_, before := openCodeModelIDs(t, home)
	retained, err := os.ReadFile(openCodeLiveModelsPath(home))
	if err != nil {
		t.Fatal(err)
	}
	valid := fixture.dependencies(t)
	for cause, deps := range map[string]openCodeModelRefreshDependencies{
		"The live model list is unavailable. Check the network connection.": {
			Fetch: func(context.Context) ([]byte, error) {
				return nil, errors.New("The live model list is unavailable. Check the network connection.")
			}, Derive: valid.Derive},
		"The live model list is invalid.": {Fetch: func(context.Context) ([]byte, error) { return []byte("<html>"), nil }, Derive: valid.Derive},
		"The live model list holds no provider of the release catalog.": {
			Fetch: func(context.Context) ([]byte, error) { return []byte(`{"fixture-provider":{"models":{"a":{}}}}`), nil }, Derive: valid.Derive},
		"The pinned OpenCode could not derive the model facts: no program": {Fetch: valid.Fetch,
			Derive: func(context.Context, []byte) ([]byte, []byte, error) {
				return nil, nil, errors.New("The pinned OpenCode could not derive the model facts: no program")
			}},
		"The model facts of OpenCode are invalid.": {Fetch: valid.Fetch,
			Derive: func(context.Context, []byte) ([]byte, []byte, error) { return fixture.baseline, []byte("[]"), nil }},
	} {
		err := refreshOpenCodeFixture(t, home, deps)
		// The agent-profile command writes the returned cause to the log of mc.
		if err == nil || err.Error() != "The refresh of the live models failed. "+cause {
			t.Fatalf("The failed refresh returned another cause: %v", err)
		}
		status, statusErr := GetAgentProfileOperation(home, openCodeModelsOperation)
		if statusErr != nil || status.Status != "failed" || status.Message != err.Error() || status.ErrorCode != "opencode_models" {
			t.Fatalf("The failed refresh recorded another state: %+v", status)
		}
		forgetOpenCodeLiveModels()
		list, ids := openCodeModelIDs(t, home)
		after, readErr := os.ReadFile(openCodeLiveModelsPath(home))
		if readErr != nil || string(after) != string(retained) || !slices.Equal(ids, before) || list.Source != OpenCodeModelSourceLive {
			t.Fatalf("The failed refresh changed the retained live models (%s).", cause)
		}
		if err := ValidateOpenCodeProfile(home, "moonshotai", "kimi-fixture-new", "high"); err != nil {
			t.Fatalf("The failed refresh caused a profile failure: %v", err)
		}
	}
}

func TestOpenCodeModelRefreshPreservesTheLiveModelOfAnEnabledProfile(t *testing.T) {
	home, _ := openCodeLiveHome(t)
	fixture := readOpenCodeLiveFixture(t)
	installOpenCodeFixture(t, home)
	if err := refreshOpenCodeFixture(t, home, fixture.dependencies(t)); err != nil {
		t.Fatal(err)
	}
	_, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), func(cfg *operatorconfig.Config) error {
		cfg.Runtimes.Bindings["selected"] = operatorconfig.RuntimeBinding{Harness: "opencode", Model: "kimi-fixture-new", Effort: "high", Credential: "catalog"}
		cfg.Runtimes.Bindings["disabled"] = operatorconfig.RuntimeBinding{Harness: "opencode", Model: "kimi-fixture-path", Credential: "catalog"}
		cfg.Runtimes.EnabledBindings = append(cfg.Runtimes.EnabledBindings, "selected")
		return cfg.Validate()
	})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := OpenCodeProvider(home, "moonshotai")
	selected, _ := before.Model("kimi-fixture-new")
	// The new live model list lacks the two models and the changed facts of kimi-k3.
	later := fixture.without(t, "kimi-fixture-new", "kimi-fixture-new-fast", "kimi-fixture-path", "kimi-k3")
	for round := 0; round < 2; round++ {
		if err := refreshOpenCodeFixture(t, home, later.dependencies(t)); err != nil {
			t.Fatal(err)
		}
		forgetOpenCodeLiveModels()
		provider, _ := OpenCodeProvider(home, "moonshotai")
		kept, ok := provider.Model("kimi-fixture-new")
		if !ok || kept.Record != selected.Record || kept.Context != selected.Context || kept.Address != selected.Address || !slices.Equal(kept.Variants, selected.Variants) {
			t.Fatalf("Refresh %d lost the facts of the live model of the enabled profile.", round)
		}
		if err := ValidateOpenCodeProfile(home, "moonshotai", "kimi-fixture-new", "high"); err != nil {
			t.Fatal(err)
		}
		if _, ok := provider.Model("kimi-fixture-path"); ok {
			t.Fatal("The refresh kept a live model that no enabled profile selects.")
		}
		if model, _ := provider.Model("kimi-k3"); model.Record != "" || model.Context != 1048576 {
			t.Fatal("The release catalog model did not return.")
		}
	}
	list, err := AgentProfileList(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range list.Profiles {
		want := map[string]string{"selected": "", "disabled": "The connection does not offer this model. Select another model."}[profile.ID]
		if (profile.ID == "selected" || profile.ID == "disabled") && profile.Problem != want {
			t.Fatalf("The profile %s shows %q.", profile.ID, profile.Problem)
		}
	}
	if _, _, err := ResolveAgentProfile(home, AgentProfileRequest{SubmissionID: "profile-live", Name: "Live", ConnectionID: "catalog", Model: "kimi-fixture-path"}); err == nil ||
		err.Error() != "The connection does not offer this model. Select another model." {
		t.Fatalf("Profile validation accepted a model that the model list lacks: %v", err)
	}
	if _, _, err := ResolveAgentProfile(home, AgentProfileRequest{SubmissionID: "profile-live", Name: "Live", ConnectionID: "catalog", Model: "kimi-fixture-new", Effort: "max"}); err != nil {
		t.Fatalf("Profile validation refused a live model: %v", err)
	}
}

func TestEnabledProfileStartsARefreshForItsLiveModelOfAnotherRelease(t *testing.T) {
	home, launches := openCodeLiveHome(t)
	fixture := readOpenCodeLiveFixture(t)
	installOpenCodeFixture(t, home)
	if err := refreshOpenCodeFixture(t, home, fixture.dependencies(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := operatorconfig.UpdateAtomic(filepath.Join(home, "config.toml"), func(cfg *operatorconfig.Config) error {
		cfg.Runtimes.Bindings["selected"] = operatorconfig.RuntimeBinding{Harness: "opencode", Model: "kimi-fixture-new", Credential: "catalog"}
		cfg.Runtimes.EnabledBindings = append(cfg.Runtimes.EnabledBindings, "selected")
		return cfg.Validate()
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	// The refresh of this test started less than one hour ago. A refresh that is not due starts at a later request.
	age := func(duration time.Duration) {
		t.Helper()
		if _, err := updateAgentProfileOperation(home, openCodeModelsOperation, func(record *agentProfileOperationRecord) error {
			record.StartedAt = time.Now().Add(-duration).UTC().Format(time.RFC3339Nano)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	age(2 * time.Hour)
	*launches = 0
	RefreshRetainedOpenCodeModels(home, cfg)
	if *launches != 0 {
		t.Fatal("A profile with an available live model started a refresh.")
	}
	previous := openCodeModelIdentity
	t.Cleanup(func() { openCodeModelIdentity = previous })
	openCodeModelIdentity = func() (string, string) { return strings.Repeat("1", 64), opencodecatalog.Release().OpenCodeVersion }
	forgetOpenCodeLiveModels()
	if err := ValidateOpenCodeProfile(home, "moonshotai", "kimi-fixture-new", ""); err == nil {
		t.Fatal("The model list holds a live model of another release catalog.")
	}
	RefreshRetainedOpenCodeModels(home, cfg)
	RefreshRetainedOpenCodeModels(home, cfg)
	if *launches != 1 {
		t.Fatalf("The profile that waits for its live model started %d refreshes.", *launches)
	}
	// A failed refresh keeps the profile in wait, and the next due request starts a refresh again.
	failed := fixture.dependencies(t)
	failed.Fetch = func(context.Context) ([]byte, error) { return nil, errors.New("The live model list is unavailable.") }
	if err := runOpenCodeModelRefreshWith(home, failed); err == nil {
		t.Fatal("The refresh did not report its failure.")
	}
	RefreshRetainedOpenCodeModels(home, cfg)
	if *launches != 1 {
		t.Fatal("A refresh started again during the hour of the failed refresh.")
	}
	age(2 * time.Hour)
	RefreshRetainedOpenCodeModels(home, cfg)
	if *launches != 2 {
		t.Fatalf("The due request started %d refreshes.", *launches)
	}
	if err := runOpenCodeModelRefreshWith(home, fixture.dependencies(t)); err != nil {
		t.Fatal(err)
	}
	forgetOpenCodeLiveModels()
	if err := ValidateOpenCodeProfile(home, "moonshotai", "kimi-fixture-new", ""); err != nil {
		t.Fatalf("The successful refresh did not return the live model: %v", err)
	}
	// The live model list of the next release lacks the model. The profile then requires repair, and no refresh starts.
	openCodeModelIdentity = func() (string, string) { return strings.Repeat("2", 64), opencodecatalog.Release().OpenCodeVersion }
	forgetOpenCodeLiveModels()
	if err := refreshOpenCodeFixture(t, home, fixture.without(t, "kimi-fixture-new", "kimi-fixture-new-fast").dependencies(t)); err != nil {
		t.Fatal(err)
	}
	forgetOpenCodeLiveModels()
	age(2 * time.Hour)
	*launches = 0
	RefreshRetainedOpenCodeModels(home, cfg)
	if err := ValidateOpenCodeProfile(home, "moonshotai", "kimi-fixture-new", ""); err == nil || *launches != 0 {
		t.Fatalf("A model that the live model list lacks started %d refreshes: %v", *launches, err)
	}
}

func TestOpenCodeProfileAwaitsARefreshOnlyForALiveModelOfAnotherRelease(t *testing.T) {
	home, _ := openCodeLiveHome(t)
	fixture := readOpenCodeLiveFixture(t)
	installOpenCodeFixture(t, home)
	if err := refreshOpenCodeFixture(t, home, fixture.dependencies(t)); err != nil {
		t.Fatal(err)
	}
	release, _ := opencodecatalog.Find("moonshotai")
	if _, ok := release.Model("kimi-k3"); !ok {
		t.Fatal("The release catalog lacks the model that the fixture replaces.")
	}
	// The release catalog model lacks this effort. The live model with the same identifier can offer it.
	replaced := ""
	for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "thinking"} {
		if release.ValidateModel("kimi-k3", effort) != nil {
			replaced = effort
			break
		}
	}
	if replaced == "" {
		t.Fatal("The release catalog model of the fixture accepts every effort.")
	}
	type profile struct{ provider, model, effort string }
	check := func(want bool, profiles ...profile) {
		t.Helper()
		for _, p := range profiles {
			if got := OpenCodeProfileAwaitsRefresh(home, p.provider, p.model, p.effort); got != want {
				t.Fatalf("The profile %+v awaits a refresh: %t.", p, got)
			}
		}
	}
	// This release derived the retained live models. A refresh restores no profile, so each fault requires repair.
	check(false, profile{"moonshotai", "kimi-fixture-new", ""}, profile{"moonshotai", "kimi-fixture-new", "minimal"},
		profile{"moonshotai", "absent-model", ""}, profile{"absent-provider", "kimi-fixture-new", ""})
	previous := openCodeModelIdentity
	t.Cleanup(func() { openCodeModelIdentity = previous })
	openCodeModelIdentity = func() (string, string) { return strings.Repeat("1", 64), opencodecatalog.Release().OpenCodeVersion }
	forgetOpenCodeLiveModels()
	// Another release derived the retained live models. A refresh can return the model and its effort levels.
	check(true, profile{"moonshotai", "kimi-fixture-new", ""}, profile{"moonshotai", "kimi-k3", replaced})
	check(false, profile{"moonshotai", "kimi-k3", ""}, profile{"moonshotai", "absent-model", ""}, profile{"absent-provider", "kimi-fixture-new", ""})
}

func TestRetainedLiveModelsMustMeetTheReleaseCatalogConditions(t *testing.T) {
	home, _ := openCodeLiveHome(t)
	fixture := readOpenCodeLiveFixture(t)
	installOpenCodeFixture(t, home)
	if err := refreshOpenCodeFixture(t, home, fixture.dependencies(t)); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(openCodeLiveModelsPath(home))
	if err != nil {
		t.Fatal(err)
	}
	var store openCodeLiveModels
	if err := json.Unmarshal(body, &store); err != nil {
		t.Fatal(err)
	}
	// A changed file cannot move a model to another origin or add a provider.
	moved := store.Providers["moonshotai"][2]
	moved.ID, moved.Address = "moved-model", "https://live-model.invalid/v1"
	store.Providers["moonshotai"] = append(store.Providers["moonshotai"], moved)
	store.Providers["fixture-provider"] = store.Providers["moonshotai"]
	if err := writeOpenCodeLiveModels(home, store); err != nil {
		t.Fatal(err)
	}
	forgetOpenCodeLiveModels()
	provider, _ := OpenCodeProvider(home, "moonshotai")
	if _, ok := provider.Model("moved-model"); ok {
		t.Fatal("The model list holds a model at another origin.")
	}
	if _, ok := provider.Model("kimi-fixture-new"); !ok {
		t.Fatal("The model list lost a valid live model.")
	}
	if _, ok := OpenCodeProvider(home, "fixture-provider"); ok {
		t.Fatal("The retained live models added a provider.")
	}
	if err := os.WriteFile(openCodeLiveModelsPath(home), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	forgetOpenCodeLiveModels()
	if _, ids := openCodeModelIDs(t, home); !slices.Equal(ids, releaseModelIDs(t)) {
		t.Fatal("An invalid file changed the model list.")
	}
}

func TestProviderAccessAcceptsALiveModelOnlyAtAReleaseCatalogOrigin(t *testing.T) {
	home, _ := openCodeLiveHome(t)
	fixture := readOpenCodeLiveFixture(t)
	installOpenCodeFixture(t, home)
	if err := refreshOpenCodeFixture(t, home, fixture.dependencies(t)); err != nil {
		t.Fatal(err)
	}
	bundle, err := newProviderTLS(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	provider, _ := OpenCodeProvider(home, "moonshotai")
	access := func(model string, path string) ProviderContainerAccess {
		selected, ok := provider.Model(model)
		if !ok {
			t.Fatalf("The model list lacks %s.", model)
		}
		return ProviderContainerAccess{Version: 1, URL: "https://host.docker.internal:4443" + path, Token: "culler_proxy_" + strings.Repeat("c", 43), CA: bundle.CA,
			Harness: "opencode", AuthMode: "api-key", OpenCode: &OpenCodeContainerAccess{Provider: "moonshotai", KeyVariable: provider.KeyVariable, WebSearch: provider.WebSearch, Model: selected}}
	}
	for _, valid := range []ProviderContainerAccess{access("kimi-fixture-new", "/v1"), access("kimi-fixture-path", "/v2"), access("kimi-k3", "/v1"), access("kimi-k2.6", "/v1")} {
		if err := validateProviderContainerAccess(valid); err != nil {
			t.Fatalf("The access of %s failed: %v", valid.OpenCode.Model.ID, err)
		}
	}
	for name, change := range map[string]func(*ProviderContainerAccess){
		"other origin":        func(a *ProviderContainerAccess) { a.OpenCode.Model.Address = "https://live-model.invalid/v1" },
		"other path":          func(a *ProviderContainerAccess) { a.URL = "https://host.docker.internal:4443/v2" },
		"other key placement": func(a *ProviderContainerAccess) { a.OpenCode.Model.KeyPlacement = "x-api-key" },
		"other package":       func(a *ProviderContainerAccess) { a.OpenCode.Model.Package = "@opencode/ai/providers/anthropic" },
		"other provider":      func(a *ProviderContainerAccess) { a.OpenCode.Provider = "moonshotai-cn" },
		"other key variable":  func(a *ProviderContainerAccess) { a.OpenCode.KeyVariable = "OTHER_KEY" },
		"release model with other facts": func(a *ProviderContainerAccess) {
			released, _ := opencodecatalog.Find("moonshotai")
			a.OpenCode.Model, _ = released.Model("kimi-k3")
			a.OpenCode.Model.Address = "https://api.moonshot.ai/v1/other"
			a.URL = "https://host.docker.internal:4443/v1/other"
		},
	} {
		invalid := access("kimi-fixture-new", "/v1")
		change(&invalid)
		if validateProviderContainerAccess(invalid) == nil {
			t.Fatalf("The access accepted a live model with a changed fact (%s).", name)
		}
	}
}

func TestLiveModelListRequestHoldsNoDeploymentData(t *testing.T) {
	var header http.Header
	requests := atomic.Int32{}
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer other.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/list":
			header = r.Header.Clone()
			_, _ = w.Write([]byte(`{"provider":{}}`))
		case "/moved":
			http.Redirect(w, r, "/list", http.StatusFound)
		case "/other":
			http.Redirect(w, r, other.URL+"/list", http.StatusFound)
		case "/large":
			_, _ = w.Write([]byte(strings.Repeat("a", 2048)))
		case "/slow":
			time.Sleep(300 * time.Millisecond)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("MC_HOME", "/operator-home-fixture")
	transport := server.Client().Transport
	body, err := fetchLiveModelList(context.Background(), transport, server.URL+"/list")
	if err != nil || string(body) != `{"provider":{}}` {
		t.Fatalf("The request failed: %v", err)
	}
	names := []string{}
	for name := range header {
		names = append(names, name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"Accept", "Accept-Encoding", "User-Agent"}) || header.Get("User-Agent") != "culler" {
		t.Fatalf("The request holds other data: %v", header)
	}
	if body, err := fetchLiveModelList(context.Background(), transport, server.URL+"/moved"); err != nil || string(body) != `{"provider":{}}` {
		t.Fatalf("The request refused its own origin: %v", err)
	}
	limit := maxOpenCodeLiveListBytes
	maxOpenCodeLiveListBytes = 1024
	defer func() { maxOpenCodeLiveListBytes = limit }()
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	for address, test := range map[string]struct {
		ctx   context.Context
		cause string
	}{
		server.URL + "/other":     {context.Background(), "The live model list address named another origin."},
		server.URL + "/large":     {context.Background(), "The live model list exceeds its size limit."},
		server.URL + "/absent":    {context.Background(), "The live model list returned HTTP 404."},
		server.URL + "/slow":      {short, "The live model list request exceeded its time limit."},
		"http://127.0.0.1:1/list": {context.Background(), "The live model list address requires HTTPS."},
	} {
		if _, err := fetchLiveModelList(test.ctx, transport, address); err == nil || err.Error() != test.cause {
			t.Fatalf("%s: %v", address, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("The request accepted a response from another origin.")
	}
	if address := opencodecatalog.LiveModelListAddress; !strings.HasPrefix(address, "https://") || strings.ContainsAny(address, "?#@") {
		t.Fatalf("The live model list address is not a fixed HTTPS address: %s", address)
	}
}

func TestOpenCodeFactsContainerHasNoNetworkKeyOrProjectData(t *testing.T) {
	home, _ := openCodeLiveHome(t)
	fixture := readOpenCodeLiveFixture(t)
	volume, err := OpenCodeVolume(home)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	run := func(_ context.Context, stdin string, args ...string) ([]byte, error) {
		calls++
		line := strings.Join(args, " ")
		for _, required := range []string{"run --rm --pids-limit=2048 -i --pull never", "--network none", "--cap-drop ALL", "--security-opt no-new-privileges", "--user 10002:10002",
			"--mount type=volume,src=" + volume + ",dst=" + OpenCodeRoot + ",readonly,volume-nocopy", "mc-prod /usr/bin/env -i PATH=/usr/local/bin:/usr/bin:/bin /bin/sh -ec"} {
			if !strings.Contains(line, required) {
				t.Errorf("The container lacks %q.", required)
			}
		}
		// The container receives its input through standard input. It mounts only the OpenCode volume.
		mounts := 0
		for index, arg := range args {
			if arg == "--mount" || arg == "-v" || arg == "--volume" {
				mounts++
			}
			if arg == "-e" || arg == "--env" || arg == "--env-file" || strings.Contains(arg, home) || strings.Contains(arg, "type=bind") {
				t.Errorf("The container receives host data: %s", args[index])
			}
		}
		if mounts != 1 {
			t.Errorf("The container has %d mounts.", mounts)
		}
		files := map[string]string{}
		reader := tar.NewReader(strings.NewReader(stdin))
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(reader)
			files[header.Name] = string(body)
		}
		plugin := opencodecatalog.FactsPlugin()
		if len(files) != 4 || files["record.json"] != "the record" || files["facts/server.mjs"] != string(plugin["server.mjs"]) || files["facts/package.json"] != string(plugin["package.json"]) {
			t.Errorf("The container input holds other files: %v", len(files))
		}
		marker := args[len(args)-1]
		if args[len(args)-2] != opencodecatalog.Release().OpenCodeVersion || !strings.HasPrefix(marker, "culler-model-facts-") {
			t.Errorf("The container does not check the pinned OpenCode: %q", args[len(args)-3:])
		}
		switch calls {
		case 1:
			return []byte("WARNING: platform\n\n" + marker + "\n" + string(fixture.baseline) + "\n" + marker + "\n" + string(fixture.live) + "\n" + marker + "\n"), nil
		case 2:
			return []byte("The installed OpenCode is opencode v0.0.1. This release requires OpenCode 2.2.7.\n"), errors.New("exit status 1")
		}
		return []byte("\n" + marker + "\nonly one report\n" + marker + "\n"), nil
	}
	if _, _, err := deriveOpenCodeModelFacts(context.Background(), home, "mc-prod", []byte("the record"), run); err == nil || err.Error() != "The pinned OpenCode is not installed." || calls != 0 {
		t.Fatalf("The derivation started without the installed OpenCode: %v", err)
	}
	installOpenCodeFixture(t, home)
	baseline, live, err := deriveOpenCodeModelFacts(context.Background(), home, "mc-prod", []byte("the record"), run)
	if err != nil || string(baseline) != string(fixture.baseline) || string(live) != string(fixture.live) {
		t.Fatalf("The derivation returned other reports: %v", err)
	}
	if _, _, err := deriveOpenCodeModelFacts(context.Background(), home, "mc-prod", []byte("the record"), run); err == nil ||
		err.Error() != "The pinned OpenCode could not derive the model facts: The installed OpenCode is opencode v0.0.1. This release requires OpenCode 2.2.7." {
		t.Fatalf("The derivation lost its cause: %v", err)
	}
	if _, _, err := deriveOpenCodeModelFacts(context.Background(), home, "mc-prod", []byte("the record"), run); err == nil {
		t.Fatal("The derivation accepted an incomplete report.")
	}
	// The derivation waits while an OpenCode installation changes the volume.
	root, _ := agentProfileOperationsRoot(home)
	installation, err := agentProfileOperationLock(root, openCodeOperation+".worker")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	before := calls
	_, _, err = deriveOpenCodeModelFacts(ctx, home, "mc-prod", []byte("the record"), run)
	agentProfileUnlock(installation)
	if err == nil || calls != before {
		t.Fatalf("The derivation read the volume during an installation: %v", err)
	}
}
