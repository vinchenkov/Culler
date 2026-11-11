package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	operatorconfig "mc/config"
	"mc/domain"
	"mc/opencodecatalog"
	"mc/routing"
	"mc/verbs"
)

// setupVerificationHome writes an operator home with one API key connection and one agent profile.
func setupVerificationHome(t *testing.T) (string, *sql.DB, operatorconfig.Config) {
	t.Helper()
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
	t.Cleanup(func() { db.Close() })
	return home, db, cfg
}

func setupVerificationStatus(t *testing.T, home string, db *sql.DB) verbs.SystemStatusResult {
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

func setupVerificationState(result verbs.SystemStatusResult, id string) string {
	for _, connection := range result.Connections {
		if connection.ID == id {
			return connection.State
		}
	}
	return ""
}

func TestSetupVerificationShowsTheTestedConnectionAsVerified(t *testing.T) {
	home, db, _ := setupVerificationHome(t)
	status := func() verbs.SystemStatusResult {
		t.Helper()
		return setupVerificationStatus(t, home, db)
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
	err := recordSetupVerification(home, "opencode-tested", false, func(action string, request any) (any, error) {
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

func TestDraftModelTestKeepsTheSavedProfileVerification(t *testing.T) {
	home, db, cfg := setupVerificationHome(t)
	verified := func(action string, request any) (any, error) {
		return verbs.ConnectionVerified(db, nil, request.(verbs.ConnectionObservation))
	}
	if err := recordSetupVerification(home, "opencode-tested", false, verified); err != nil {
		t.Fatal(err)
	}
	const profileScope = "connection:opencode-tested:model_access:opencode-tested"
	if state := setupVerificationState(setupVerificationStatus(t, home, db), profileScope); state != "ready" {
		t.Fatalf("The saved agent profile is %q before the draft test", state)
	}
	saved := cfg.Runtimes.Bindings["opencode-tested"]
	draft := saved
	draft.Model = "draft-model"
	cfg.Runtimes.Bindings["opencode-tested"] = draft
	draftRevision := routing.ProfileRevision(routing.ConfiguredBindings(cfg.Runtimes)["opencode-tested"])
	cfg.Runtimes.Bindings["opencode-tested"] = saved
	record := func() []verbs.ConnectionCheck {
		t.Helper()
		required, err := systemRequirementsAt(home)
		if err != nil {
			t.Fatal(err)
		}
		for _, connection := range required.Connections {
			if connection.ID != "opencode-tested" {
				continue
			}
			scopes := modelVerificationScopes(connection, "model_test", connection.AuthorizationID, "opencode-tested", draftRevision)
			observed := time.Now().UTC().Format(time.RFC3339Nano)
			for _, scope := range scopes {
				scope.CheckID = domain.DiagnosticID()
				if _, err := verified("verified", verbs.ConnectionObservation{ConnectionCheck: scope, EventID: domain.DiagnosticID(), Outcome: "ready", ObservedAt: observed}); err != nil {
					t.Fatal(err)
				}
			}
			return scopes
		}
		t.Fatal("The connection is absent from the system requirements")
		return nil
	}
	for _, scope := range record() {
		if scope.Operation == "model_access" {
			t.Fatalf("The draft test verifies the saved agent profile record: %+v", scope)
		}
	}
	if state := setupVerificationState(setupVerificationStatus(t, home, db), profileScope); state != "ready" {
		t.Fatalf("The draft test changed the saved agent profile to %q", state)
	}
	cfg.Runtimes.Bindings["opencode-tested"] = draft
	if _, err := operatorconfig.WriteAtomic(filepath.Join(home, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}
	if state := setupVerificationState(setupVerificationStatus(t, home, db), profileScope); state != "unknown" {
		t.Fatalf("The saved draft is %q before its test result is recorded", state)
	}
	record()
	if state := setupVerificationState(setupVerificationStatus(t, home, db), profileScope); state != "ready" {
		t.Fatalf("The saved draft is %q after its test result is recorded", state)
	}
}
