package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	operatorconfig "mc/config"
	"mc/opencodecatalog"
	"mc/routing"
	"mc/verbs"
)

func TestSetupVerificationShowsTheTestedConnectionAsVerified(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_HOME", home)
	spine := filepath.Join(home, "spine.db")
	if _, err := verbs.Init(verbs.InitArgs{Spine: spine, Project: "ws-test", WorkspaceRoot: filepath.Join(home, "workspace")}); err != nil {
		t.Fatal(err)
	}
	provider := opencodecatalog.Release().Providers[0]
	cfg := operatorconfig.Default()
	cfg.Runtimes.Credentials["opencode-tested"] = operatorconfig.CredentialSource{Name: "Tested provider", Kind: operatorconfig.CredentialKindOpenCodeAPIKey, CatalogEntry: provider.ID}
	cfg.Runtimes.Bindings["opencode-tested"] = operatorconfig.RuntimeBinding{Name: "Tested provider", Harness: "opencode", Model: provider.Models[0].ID, Credential: "opencode-tested"}
	if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	table, err := routing.UniformTable("opencode-tested", routing.Registry{"opencode-tested": "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "routing.md"), []byte(table.Markdown()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "refresh-grants"), 0o700); err != nil {
		t.Fatal(err)
	}
	reference, _ := json.Marshal(map[string]any{"version": 1, "credential": "opencode-tested", "channel": "opencode", "type": "api_key",
		"authorization_id": strings.Repeat("a", 64), "credential_ref": strings.Repeat("b", 64)})
	if err := os.WriteFile(filepath.Join(home, "refresh-grants", "opencode-tested.json"), reference, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := verbs.OpenSpine(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	status := func() verbs.SystemStatusResult {
		t.Helper()
		required, err := systemRequirementsAt(home)
		if err != nil {
			t.Fatal(err)
		}
		result, err := verbs.SystemStatus(db, nil, required)
		if err != nil {
			t.Fatal(err)
		}
		return result.(verbs.SystemStatusResult)
	}
	issues := func(result verbs.SystemStatusResult) int {
		count := 0
		for _, condition := range result.Conditions {
			if condition.ConnectionID == "opencode-tested" {
				count++
			}
		}
		return count
	}
	if issues(status()) == 0 {
		t.Fatal("The untested connection does not await verification")
	}
	err = recordSetupVerification(home, "opencode-tested", false, func(action string, request any) (any, error) {
		if action != "verified" {
			t.Fatalf("The setup test record used the %s action", action)
		}
		return verbs.ConnectionVerified(db, nil, request.(verbs.ConnectionObservation))
	})
	if err != nil {
		t.Fatal(err)
	}
	result := status()
	scopes := map[string]string{}
	for _, connection := range result.Connections {
		if connection.ConnectionID == "opencode-tested" {
			scopes[connection.ID] = connection.State
		}
	}
	if issues(result) != 0 || scopes["connection:opencode-tested:connection_access:"] != "ready" || scopes["connection:opencode-tested:model_access:opencode-tested"] != "ready" {
		t.Fatalf("The passing setup test did not verify the connection and its agent profile: %v", scopes)
	}
}
