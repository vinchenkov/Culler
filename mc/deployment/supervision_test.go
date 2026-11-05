package deployment

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func supervisionFixture(t *testing.T) (string, SupervisionSpec) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "mc-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	home, err := CanonicalHome(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{
		filepath.Join(home, "release"), filepath.Join(home, "release", "runner"),
		filepath.Join(home, "release", "host"),
	} {
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range runnerReleaseDirs[1:] {
		if err := os.Mkdir(filepath.Join(home, "release", "runner", filepath.FromSlash(rel)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range runnerReleaseFiles {
		if err := os.WriteFile(filepath.Join(home, "release", "runner", filepath.FromSlash(rel)), []byte("// runner\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range hostReleaseDirs[1:] {
		if err := os.Mkdir(filepath.Join(home, "release", "host", filepath.FromSlash(rel)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range hostReleaseFiles {
		if err := os.WriteFile(filepath.Join(home, "release", "host", filepath.FromSlash(rel)), []byte("// host\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, name := range []string{"mc", "bun", "docker"} {
		path := filepath.Join(bin, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		paths[name] = resolved
	}
	spec := SupervisionSpec{
		ReleaseBuildID: "0123456789abcdef0123456789abcdef01234567",
		MCPath:         paths["mc"], BunPath: paths["bun"], DockerPath: paths["docker"],
		Image:          "mc-prod",
		WorkspaceRoots: []WorkspaceRoot{{ID: "alpha", Root: workspace}},
		SupervisionPolicy: SupervisionPolicy{
			DashboardBind: "127.0.0.1:7333", DashboardAuthToken: "fixture-token",
			AgentsViewBind: "127.0.0.1:7334", WikiBind: "127.0.0.1:7335",
			BackupIntervalS: 3600, TickIntervalS: 60,
			GitAuthorName: "Culler", GitAuthorEmail: "mc@localhost",
			RuntimeBindings: map[string]ResidentRuntimeBinding{
				"claude": {
					Harness: "claude-sdk", Channel: "claude",
					TokenURL: "https://platform.claude.com/v1/oauth/token",
					ClientID: "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
				},
				"endpoint": {
					Harness: "claude-sdk", Channel: "claude",
					TokenURL: "https://platform.claude.com/v1/oauth/token",
					ClientID: "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
				},
			},
		},
	}
	names, err := RuntimeNames(home)
	if err != nil {
		t.Fatal(err)
	}
	spec.SpineVolume = names.Volume
	return home, spec
}

func TestPrepareSupervisionPublishesClosedUnloadedLaunchAgentBundle(t *testing.T) {
	home, spec := supervisionFixture(t)
	inspected := []string{}
	status, result, err := PrepareSupervision(home, spec, func(label string) (bool, error) {
		inspected = append(inspected, label)
		return false, nil
	})
	if err != nil {
		t.Fatalf("prepare supervision: %v", err)
	}
	if status != "done" || len(inspected) != 6 {
		t.Fatalf("status=%q inspected=%v", status, inspected)
	}
	names, _ := RuntimeNames(home)
	wantLabels := []string{"com.culler." + names.Suffix + ".resident", "com.culler." + names.Suffix + ".dashboard"}
	if result.ResidentLabel != wantLabels[0] || result.DashboardLabel != wantLabels[1] ||
		result.AgentsViewLabel != "com.culler."+names.Suffix+".agentsview" {
		t.Fatalf("labels=%+v", result)
	}

	root := filepath.Join(home, "supervision")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	if strings.Join(got, ",") != "LaunchAgents,agentsview.toml,dashboard.json,resident.json" {
		t.Fatalf("supervision entries=%v", got)
	}
	for _, rel := range []string{"resident.json", "dashboard.json", "agentsview.toml", "LaunchAgents/resident.plist", "LaunchAgents/dashboard.plist", "LaunchAgents/agentsview.plist"} {
		info, err := os.Lstat(filepath.Join(root, rel))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s info=%v err=%v", rel, info, err)
		}
	}

	var resident map[string]any
	body, _ := os.ReadFile(filepath.Join(root, "resident.json"))
	if err := json.Unmarshal(body, &resident); err != nil {
		t.Fatal(err)
	}
	if resident["releaseBuildId"] != spec.ReleaseBuildID || resident["mcPath"] != spec.MCPath || resident["dockerPath"] != spec.DockerPath || resident["runnerSrcDir"] != filepath.Join(home, "release", "runner") || resident["backupIntervalMs"] != float64(3600000) ||
		resident["gitAuthorName"] != spec.GitAuthorName ||
		resident["gitAuthorEmail"] != spec.GitAuthorEmail {
		t.Fatalf("resident config=%v", resident)
	}
	roots := resident["workspaceRoots"].([]any)
	if len(roots) != 1 || roots[0].(map[string]any)["id"] != "alpha" {
		t.Fatalf("workspace roots=%v", roots)
	}

	for i, label := range wantLabels {
		name := []string{"resident.plist", "dashboard.plist"}[i]
		body, err := os.ReadFile(filepath.Join(root, "LaunchAgents", name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		// MC_LOG_LEVEL is the ONLY way to move a supervised unit off the default
		// level: config.toml holds no observability block, so if it is not in
		// the generated environment dict the operator's only recourse is editing
		// a generated file by hand.
		for _, required := range []string{"<key>RunAtLoad</key>", "<true/>", "<key>KeepAlive</key>", label, spec.BunPath, filepath.Join(home, "logs"),
			"<key>MC_LOG_LEVEL</key>\n    <string>info</string>"} {
			if !strings.Contains(text, required) {
				t.Fatalf("%s missing %q", label, required)
			}
		}
		decoder := xml.NewDecoder(strings.NewReader(text))
		for {
			_, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("invalid property list %s: %v", name, err)
			}
		}
	}

	before, _ := os.Lstat(root)
	status, _, err = PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil })
	after, _ := os.Lstat(root)
	if err != nil || status != "ok" || !os.SameFile(before, after) {
		t.Fatalf("idempotent replay status=%q same=%v err=%v", status, os.SameFile(before, after), err)
	}
}

func TestPrepareSupervisionProjectsConfiguredDashboardAndBackupPolicy(t *testing.T) {
	home, spec := supervisionFixture(t)
	spec.DashboardBind = "0.0.0.0:7443"
	spec.DashboardAuthToken = "dashboard-secret"
	spec.BackupIntervalS = 17
	spec.TickIntervalS = 45
	if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) {
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
	var dashboard map[string]string
	body, err := os.ReadFile(filepath.Join(home, "supervision", "dashboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &dashboard); err != nil {
		t.Fatal(err)
	}
	if dashboard["bind"] != spec.DashboardBind ||
		dashboard["auth_token"] != spec.DashboardAuthToken ||
		dashboard["wiki_bind"] != spec.WikiBind {
		t.Fatalf("dashboard config = %#v", dashboard)
	}
	var resident map[string]any
	body, err = os.ReadFile(filepath.Join(home, "supervision", "resident.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &resident); err != nil {
		t.Fatal(err)
	}
	if resident["backupIntervalMs"] != float64(17000) {
		t.Fatalf("backup interval = %v", resident["backupIntervalMs"])
	}
	if resident["agentsViewBind"] != spec.AgentsViewBind {
		t.Fatalf("AgentsView bind = %v", resident["agentsViewBind"])
	}
	// The metronome is an operator knob. Without it in the written config the
	// resident falls back to its own 60_000 default.
	if resident["tickIntervalMs"] != float64(45000) {
		t.Fatalf("tick interval = %v", resident["tickIntervalMs"])
	}
	if resident["gitAuthorName"] != spec.GitAuthorName ||
		resident["gitAuthorEmail"] != spec.GitAuthorEmail {
		t.Fatalf("git identity = %#v", resident)
	}
	// The enabled catalog reaches the resident, and only the channel authority
	// does: no secret, and no model (that rides the dispatch effect).
	catalog, ok := resident["runtimeBindings"].(map[string]any)
	if !ok {
		t.Fatalf("resident config carries no runtime binding catalog: %#v", resident)
	}
	claude, ok := catalog["claude"].(map[string]any)
	if !ok || claude["channel"] != "claude" || claude["harness"] != "claude-sdk" ||
		claude["token_url"] == "" || claude["client_id"] == "" {
		t.Fatalf("claude binding = %#v", catalog["claude"])
	}
	if _, leaked := claude["model"]; leaked {
		t.Fatalf("the resident catalog carries a model: %#v", claude)
	}
	account, ok := catalog["endpoint"].(map[string]any)
	if !ok || account["channel"] != "claude" || account["token_url"] == "" || account["client_id"] == "" {
		t.Fatalf("The sign-in account lost its renewal authority: %#v", account)
	}
	for _, field := range []string{"env_name", "base_url", "secret"} {
		if _, present := account[field]; present {
			t.Fatalf("The account includes %s.", field)
		}
	}

	matches, err := SupervisionPolicyMatches(home, spec.SupervisionPolicy)
	if err != nil || !matches {
		t.Fatalf("projected policy match = %v, err %v", matches, err)
	}
	changedCatalog := spec.SupervisionPolicy
	changedCatalog.RuntimeBindings = map[string]ResidentRuntimeBinding{}
	matches, err = SupervisionPolicyMatches(home, changedCatalog)
	if err != nil || !matches {
		t.Fatalf("A runtime catalog change required a service restart: %v", err)
	}
	for name, drift := range map[string]func(*SupervisionPolicy){
		"rotated dashboard token": func(p *SupervisionPolicy) { p.DashboardAuthToken = "rotated-secret" },
		"drifted Git identity":    func(p *SupervisionPolicy) { p.GitAuthorName = "Drifted Role" },
		"retuned backup interval": func(p *SupervisionPolicy) { p.BackupIntervalS = 99 },
		"retuned tick interval":   func(p *SupervisionPolicy) { p.TickIntervalS = 99 },
		"moved AgentsView":        func(p *SupervisionPolicy) { p.AgentsViewBind = "127.0.0.1:7344" },
		"moved wiki address":      func(p *SupervisionPolicy) { p.WikiBind = "127.0.0.1:7345" },
	} {
		drifted := spec.SupervisionPolicy
		drift(&drifted)
		matches, err = SupervisionPolicyMatches(home, drifted)
		if err != nil || matches {
			t.Fatalf("%s policy match = %v, err %v", name, matches, err)
		}
	}
}

func TestPrepareSupervisionRetainsPublishedImageDigest(t *testing.T) {
	home, spec := supervisionFixture(t)
	spec.Image = "ghcr.io/example/culler@sha256:" + strings.Repeat("a", 64)
	if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(home, "supervision", "resident.json"))
	if err != nil {
		t.Fatal(err)
	}
	var resident struct {
		Image string `json:"image"`
	}
	if err := json.Unmarshal(body, &resident); err != nil || resident.Image != spec.Image {
		t.Fatalf("resident image=%q want=%q err=%v", resident.Image, spec.Image, err)
	}
	for _, image := range []string{"ghcr.io/example/culler:latest", "ghcr.io/example/culler@sha256:" + strings.Repeat("a", 63), "ghcr.io/example/culler@sha256:" + strings.Repeat("A", 64)} {
		spec.Image = image
		if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil }); err == nil {
			t.Fatalf("accepted image %q", image)
		}
	}
}

func TestSupervisionKeepsDiscordCredentialsInTheHostConfiguration(t *testing.T) {
	home, spec := supervisionFixture(t)
	if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	var resident map[string]any
	body, err := os.ReadFile(filepath.Join(home, "supervision", "resident.json"))
	if err != nil || json.Unmarshal(body, &resident) != nil {
		t.Fatalf("resident config read: %v", err)
	}
	if _, exists := resident["discord"]; exists {
		t.Fatal("The supervision file contains Discord credentials.")
	}
	matches, err := SupervisionPolicyMatches(home, spec.SupervisionPolicy)
	if err != nil || !matches {
		t.Fatalf("projected policy match = %v, err %v", matches, err)
	}
}

func TestPrepareSupervisionRefusesAnyDashboardBindWithoutAuth(t *testing.T) {
	for _, bind := range []string{"127.0.0.1:7333", "0.0.0.0:7443"} {
		home, spec := supervisionFixture(t)
		spec.DashboardBind = bind
		spec.DashboardAuthToken = ""
		if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) {
			return false, nil
		}); err == nil || !strings.Contains(err.Error(), "dashboard auth") {
			t.Fatalf("bind %s error = %v", bind, err)
		}
		if _, err := os.Lstat(filepath.Join(home, "supervision")); !os.IsNotExist(err) {
			t.Fatalf("bind %s refusal published supervision: %v", bind, err)
		}
	}
}

func TestLaunchdTCCGateRejectsProtectedUserFolders(t *testing.T) {
	userHome := filepath.Join(t.TempDir(), "operator")
	for _, name := range []string{"Documents", "Desktop", "Downloads"} {
		root := filepath.Join(userHome, name)
		for _, candidate := range []string{root, filepath.Join(root, "culler")} {
			err := validateLaunchdTCCPath(candidate, []string{
				filepath.Join(userHome, "Documents"),
				filepath.Join(userHome, "Desktop"),
				filepath.Join(userHome, "Downloads"),
			})
			if err == nil || !strings.Contains(err.Error(), "TCC-protected") {
				t.Fatalf("%s candidate %q error = %v", name, candidate, err)
			}
		}
	}
	safe := filepath.Join(userHome, "Library", "Application Support", "culler")
	if err := validateLaunchdTCCPath(safe, []string{
		filepath.Join(userHome, "Documents"),
		filepath.Join(userHome, "Desktop"),
		filepath.Join(userHome, "Downloads"),
	}); err != nil {
		t.Fatalf("safe launchd home refused: %v", err)
	}
}

func TestPrepareSupervisionRefusesTCCProtectedHomeBeforePublication(t *testing.T) {
	operatorHome := t.TempDir()
	t.Setenv("HOME", operatorHome)
	protected := filepath.Join(operatorHome, "Documents")
	if err := os.Mkdir(protected, 0o700); err != nil {
		t.Fatal(err)
	}
	home, spec := supervisionFixture(t)
	target := filepath.Join(protected, "mc-home")
	if err := os.Rename(home, target); err != nil {
		t.Fatal(err)
	}
	names, err := RuntimeNames(target)
	if err != nil {
		t.Fatal(err)
	}
	spec.SpineVolume = names.Volume
	if _, _, err := PrepareSupervision(target, spec, func(string) (bool, error) {
		return false, nil
	}); err == nil || !strings.Contains(err.Error(), "TCC-protected") {
		t.Fatalf("protected supervision error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(target, "supervision")); !os.IsNotExist(err) {
		t.Fatalf("TCC refusal published supervision: %v", err)
	}
}

func TestPrepareSupervisionRefusesLoadedOrUnsafeInputsBeforePublication(t *testing.T) {
	t.Run("loaded", func(t *testing.T) {
		home, spec := supervisionFixture(t)
		_, _, err := PrepareSupervision(home, spec, func(label string) (bool, error) {
			return strings.HasSuffix(label, ".resident"), nil
		})
		if err == nil || !strings.Contains(err.Error(), "already loaded") {
			t.Fatalf("err=%v", err)
		}
		if _, statErr := os.Lstat(filepath.Join(home, "supervision")); !os.IsNotExist(statErr) {
			t.Fatalf("loaded refusal published supervision: %v", statErr)
		}
	})

	t.Run("release identity", func(t *testing.T) {
		home, spec := supervisionFixture(t)
		spec.ReleaseBuildID = "development"
		if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil }); err == nil {
			t.Fatal("development identity accepted for production supervision")
		}
	})

	t.Run("workspace alias", func(t *testing.T) {
		home, spec := supervisionFixture(t)
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(spec.WorkspaceRoots[0].Root, alias); err != nil {
			t.Fatal(err)
		}
		spec.WorkspaceRoots[0].Root = alias
		if _, _, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil }); err == nil {
			t.Fatal("workspace symlink accepted")
		}
	})
}

func TestPrepareSupervisionWithoutProjects(t *testing.T) {
	home, spec := supervisionFixture(t)
	spec.WorkspaceRoots = nil
	_, result, err := PrepareSupervision(home, spec, func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(result.Root, "resident.json"))
	if err != nil {
		t.Fatal(err)
	}
	var resident map[string]any
	if err := json.Unmarshal(body, &resident); err != nil {
		t.Fatal(err)
	}
	roots, ok := resident["workspaceRoots"].([]any)
	if !ok || len(roots) != 0 || resident["workspaceRoot"] != "" {
		t.Fatalf("empty Project configuration: %s", body)
	}
}
