package deployment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageRuntimeAuthSourcesCopiesBothSignInsIntoDisposableFence(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSignInCatalogConfig(t, home)
	sources := RuntimeAuthSources{
		Bindings: []string{"chatgpt", "endpoint"},
	}
	acquisition := signInFixture(t, home, signInCodexFixture, signInClaudeFixture)
	stage, err := StageRuntimeAuthSources(home, sources, acquisition)
	if err != nil {
		t.Fatal(err)
	}
	if err := acquisition.Cleanup(); err != nil {
		t.Fatal(err)
	}
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		stage.Sources.CodexAuthFile,
		stage.Sources.ClaudeCredentialsFile,
	} {
		rel, err := filepath.Rel(
			runtimeAuthSourceRoot(canonicalHome), path)
		if err != nil || strings.HasPrefix(rel, "..") {
			t.Fatalf("staged path %q escaped source root: %q, %v", path, rel, err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("staged source %q mode = %v, %v", path, info, err)
		}
	}
	if body, err := os.ReadFile(stage.Sources.CodexAuthFile); err != nil || string(body) != signInCodexFixture {
		t.Fatalf("the stage lost the sign-in credential: %v", err)
	}
	if err := stage.Cleanup(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(runtimeAuthSourceRoot(canonicalHome))
	if err != nil || len(entries) != 0 {
		t.Fatalf("source stage survived cleanup: %v, %v", entries, err)
	}
}

func TestStageRuntimeAuthSourcesCopiesOnlyTheClaudeSubscriptionCredential(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSignInCatalogConfig(t, home)
	subscription := `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":9999999999999,"scopes":["user:inference"]}}`
	store := strings.TrimSuffix(subscription, "}") + `,"mcpOAuth":{"fixture|0123":{"accessToken":"fixture-mcp-secret"}}}`
	stage, err := StageRuntimeAuthSources(home, RuntimeAuthSources{Bindings: []string{"claude"}}, signInFixture(t, home, "", store))
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Cleanup()
	staged, err := os.ReadFile(stage.Sources.ClaudeCredentialsFile)
	if err != nil || string(staged) != subscription {
		t.Fatalf("the stage holds more than the subscription credential: %v", err)
	}
}

func TestRuntimeAuthSourceStageCleanupFollowsItsRetainedIdentityAfterRename(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSignInCatalogConfig(t, home)
	acquisition := signInFixture(t, home, "", signInClaudeFixture)
	stage, err := StageRuntimeAuthSources(home, RuntimeAuthSources{
		Bindings: []string{"endpoint"},
	}, acquisition)
	if cleanupErr := acquisition.Cleanup(); cleanupErr != nil {
		t.Fatal(cleanupErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(stage.sourceRoot, ".provider-import-renamed")
	if err := os.Rename(stage.stageRoot, renamed); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportStagedRuntimeAuth(
		home, stage,
		RuntimeAuthVerifyFunc(func(string, string) error { return nil })); err != nil {
		t.Fatalf("descriptor-bound import refused an unambiguous rename: %v", err)
	}
	if err := stage.Cleanup(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(stage.sourceRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("renamed source stage survived cleanup: %v, %v", entries, err)
	}
}

func TestRuntimeAuthSourceStageRefusesAnInParentPathReplacement(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSignInCatalogConfig(t, home)
	acquisition := signInFixture(t, home, "", signInClaudeFixture)
	stage, err := StageRuntimeAuthSources(home, RuntimeAuthSources{
		Bindings: []string{"endpoint"},
	}, acquisition)
	if cleanupErr := acquisition.Cleanup(); cleanupErr != nil {
		t.Fatal(cleanupErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(stage.sourceRoot, ".provider-import-renamed")
	originalPath := stage.stageRoot
	if err := os.Rename(originalPath, renamed); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(originalPath, 0o700); err != nil {
		t.Fatal(err)
	}
	replacement := writeSecretFile(t, originalPath, "endpoint-token", "replacement\n")
	_, err = ImportStagedRuntimeAuth(
		home, stage,
		RuntimeAuthVerifyFunc(func(string, string) error { return nil }))
	if err == nil || !strings.Contains(err.Error(), "original path was replaced") {
		t.Fatalf("replacement import = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "refresh-grants")); !os.IsNotExist(statErr) {
		t.Fatalf("replacement source published grants: %v", statErr)
	}
	err = stage.Cleanup()
	if err == nil || !strings.Contains(err.Error(), "original path was replaced") {
		t.Fatalf("replacement cleanup = %v", err)
	}
	if body, readErr := os.ReadFile(replacement); readErr != nil || string(body) != "replacement\n" {
		t.Fatalf("cleanup touched replacement source: %q, %v", body, readErr)
	}
	if _, statErr := os.Stat(renamed); !os.IsNotExist(statErr) {
		t.Fatalf("retained original stage survived cleanup: %v", statErr)
	}
}

func TestRuntimeAuthSourceStageCleanupNeverRecursesIntoAPathReplacement(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSignInCatalogConfig(t, home)
	acquisition := signInFixture(t, home, "", signInClaudeFixture)
	stage, err := StageRuntimeAuthSources(home, RuntimeAuthSources{
		Bindings: []string{"endpoint"},
	}, acquisition)
	if cleanupErr := acquisition.Cleanup(); cleanupErr != nil {
		t.Fatal(cleanupErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "moved-original-stage")
	originalPath := stage.stageRoot
	if err := os.Rename(originalPath, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(originalPath, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := writeSecretFile(t, originalPath, "operator-data", "keep\n")
	err = stage.Cleanup()
	if err == nil || !strings.Contains(err.Error(), "identity is no longer linked") {
		t.Fatalf("replaced-path cleanup = %v", err)
	}
	if body, readErr := os.ReadFile(marker); readErr != nil || string(body) != "keep\n" {
		t.Fatalf("cleanup touched replacement tree: %q, %v", body, readErr)
	}
	entries, readErr := os.ReadDir(moved)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("retained original stage still holds credentials: %v, %v", entries, readErr)
	}
}

func TestStageRuntimeAuthSourcesSharesSignInAcrossDistinctConnections(t *testing.T) {
	home := agentProfileTestHome(t)
	writeSignInCatalogConfig(t, home)
	acquisition := signInFixture(t, home, signInCodexFixture, signInClaudeFixture)
	stage, err := StageRuntimeAuthSources(home, RuntimeAuthSources{Bindings: []string{"chatgpt", "claude", "endpoint"}}, acquisition)
	if err != nil {
		t.Fatal(err)
	}
	if err := acquisition.Cleanup(); err != nil {
		t.Fatal(err)
	}
	defer stage.Cleanup()
	if _, err := ImportStagedRuntimeAuth(home, stage, RuntimeAuthVerifyFunc(func(string, string) error { return nil })); err != nil {
		t.Fatal(err)
	}
	grants := readGrantMap(t, filepath.Join(home, "refresh-grants"))
	if len(grants) != 3 || grants["claude"]["credential"] != "claude" || grants["endpoint"]["credential"] != "endpoint" {
		t.Fatal("The sign-in lost a connection identity.")
	}
}
