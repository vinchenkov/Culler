package deployment

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"mc/opencodecatalog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type WorkspaceRoot struct {
	ID   string `json:"id"`
	Root string `json:"root"`
}

func validSupervisionImage(image string) bool {
	if image == "mc-prod" {
		return true
	}
	name, digest, ok := strings.Cut(image, "@sha256:")
	return ok && strings.HasPrefix(name, "ghcr.io/") && len(digest) == 64 && validProductionBuildID(digest) &&
		!strings.ContainsAny(name, " \t\r\n\x00:@")
}

// ResidentRuntimeBinding identifies a sign-in account and its release authority.
// The profile supplies the model through the dispatch effect.
type ResidentRuntimeBinding struct {
	OpenCode     *opencodecatalog.Provider `json:"opencode,omitempty"`
	Kind         string                    `json:"kind"`
	InferenceURL string                    `json:"inference_url"`
	Model        string                    `json:"model,omitempty"`
	Required     bool                      `json:"required,omitempty"`
	Harness      string                    `json:"harness"`
	Credential   string                    `json:"credential"`
	Channel      string                    `json:"channel"`
	TokenURL     string                    `json:"token_url,omitempty"`
	ClientID     string                    `json:"client_id,omitempty"`
}

// SupervisionPolicy is the config.toml half of a supervision bundle: every
// operator value that the written unit configurations hold. It is one struct
// rather than a parameter list because the bundle is written from it AND
// rechecked against it, and an operator knob that reaches only one of those
// two is a loaded unit silently disagreeing with config.toml.
type SupervisionPolicy struct {
	DashboardBind      string
	DashboardAuthToken string
	BackupIntervalS    int
	TickIntervalS      int
	GitAuthorName      string
	GitAuthorEmail     string
	// RuntimeBindings is config.toml's ENABLED catalog. The resident used to
	// compile its own copy of these channels and OAuth authorities, so an
	// operator-defined binding reached it and then failed credential
	// projection; it is deployment input now.
	RuntimeBindings map[string]ResidentRuntimeBinding
	AgentsViewBind  string
	WikiBind        string
}

type SupervisionSpec struct {
	ReleaseBuildID string
	MCPath         string
	BunPath        string
	DockerPath     string
	Image          string
	ProjectImage   string
	SpineVolume    string
	WorkspaceRoots []WorkspaceRoot
	SupervisionPolicy
}

type SupervisionResult struct {
	ResidentLabel   string
	DashboardLabel  string
	AgentsViewLabel string
	Root            string
}

// SupervisionPolicyMatches reports whether an already-loaded bundle carries
// the current host config projection. Callers must refuse a loaded mismatch;
// offline replacement is intentionally prohibited.
// SupervisionReleaseMatches reports whether the prepared resident config was
// written for releaseBuildID. It is the release half of the drift question the
// policy comparison answers for config.toml: activation refuses a resident
// config from another release, so an upgrade has to be able to SEE that the
// prepared bundle is stale before it decides whether to rewrite it.
func SupervisionReleaseMatches(home string, releaseBuildID string) (bool, error) {
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return false, err
	}
	root := filepath.Join(canonicalHome, "supervision")
	if err := validateSupervisionTree(root); err != nil {
		return false, err
	}
	body, err := os.ReadFile(filepath.Join(root, "resident.json"))
	if err != nil {
		return false, err
	}
	var resident map[string]any
	if err := json.Unmarshal(body, &resident); err != nil {
		return false, err
	}
	return resident["releaseBuildId"] == releaseBuildID, nil
}

func SupervisionPolicyMatches(home string, policy SupervisionPolicy) (bool, error) {
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return false, err
	}
	root := filepath.Join(canonicalHome, "supervision")
	if err := validateSupervisionTree(root); err != nil {
		return false, err
	}
	var dashboard map[string]any
	body, err := os.ReadFile(filepath.Join(root, "dashboard.json"))
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(body, &dashboard); err != nil {
		return false, err
	}
	if dashboard["bind"] != policy.DashboardBind {
		return false, nil
	}
	if token, hasToken := dashboard["auth_token"]; !hasToken || token != policy.DashboardAuthToken {
		return false, nil
	}
	if dashboard["wiki_bind"] != policy.WikiBind {
		return false, nil
	}
	if key, err := ReadWikiKey(canonicalHome); err != nil || dashboard["wiki_key"] != key {
		return false, nil
	}
	var resident map[string]any
	body, err = os.ReadFile(filepath.Join(root, "resident.json"))
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(body, &resident); err != nil {
		return false, err
	}
	if resident["backupIntervalMs"] != float64(policy.BackupIntervalS*1000) ||
		resident["tickIntervalMs"] != float64(policy.TickIntervalS*1000) ||
		resident["gitAuthorName"] != policy.GitAuthorName ||
		resident["gitAuthorEmail"] != policy.GitAuthorEmail ||
		resident["agentsViewBind"] != policy.AgentsViewBind {
		return false, nil
	}
	names, err := RuntimeNames(canonicalHome)
	if err != nil {
		return false, err
	}
	if matches, err := agentsViewBundleMatches(root, canonicalHome, names, policy.AgentsViewBind); err != nil || !matches {
		return false, err
	}
	return true, nil
}

type LaunchdLoadedProbe func(label string) (bool, error)

var supervisionFiles = []string{
	"LaunchAgents/agentsview.plist",
	"LaunchAgents/dashboard.plist",
	"LaunchAgents/resident.plist",
	"agentsview.toml",
	"dashboard.json",
	"resident.json",
}

func validProductionBuildID(value string) bool {
	if len(value) < 40 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func canonicalExecutable(path, label string) (string, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("%s must be an absolute executable path", label)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%s must resolve to an executable regular file", label)
	}
	return filepath.Clean(resolved), nil
}

func validateWorkspaceRoots(roots []WorkspaceRoot) ([]WorkspaceRoot, error) {
	out := append([]WorkspaceRoot{}, roots...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	for i := range out {
		if out[i].ID == "" || len(out[i].ID) > 128 {
			return nil, fmt.Errorf("supervision workspace id is invalid")
		}
		for j, r := range out[i].ID {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
				(j > 0 && (r == '.' || r == '_' || r == '-')) {
				continue
			}
			return nil, fmt.Errorf("supervision workspace id %q is invalid", out[i].ID)
		}
		if i > 0 && out[i-1].ID == out[i].ID {
			return nil, fmt.Errorf("supervision workspace id %q is duplicated", out[i].ID)
		}
		if out[i].Root == "" || !filepath.IsAbs(out[i].Root) || filepath.Clean(out[i].Root) != out[i].Root {
			return nil, fmt.Errorf("Project %s workspace root is not a clean absolute path", out[i].ID)
		}
		resolved, err := filepath.EvalSymlinks(out[i].Root)
		if err != nil || filepath.Clean(resolved) != out[i].Root {
			return nil, fmt.Errorf("Project %s workspace root is unavailable or changed identity", out[i].ID)
		}
		info, err := os.Stat(out[i].Root)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("Project %s workspace root is not a directory", out[i].ID)
		}
	}
	return out, nil
}

func xmlText(value string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}

// launchAgent writes the plist of one Bun unit, the resident or the dashboard.
// MC_LOG_LEVEL is in the environment dict rather than in a config file because
// config.toml holds no observability block: the level is an environment knob,
// the same way MC_TICK_INTERVAL_MS is. Without it here the two Bun units could
// only be moved off the default by editing a generated plist by hand.
func launchAgent(label, bun, entry, config, working, mcHome, logLevel, pathEnv, stdout, stderr string) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>%s</string>
    <string>--config</string>
    <string>%s</string>
  </array>
  <key>WorkingDirectory</key>
  <string>%s</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>MC_HOME</key>
    <string>%s</string>
    <key>MC_LOG_LEVEL</key>
    <string>%s</string>
    <key>PATH</key>
    <string>%s</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Standard</string>
  <key>ThrottleInterval</key>
  <integer>10</integer>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, xmlText(label), xmlText(bun), xmlText(entry), xmlText(config), xmlText(working),
		xmlText(mcHome), xmlText(logLevel), xmlText(pathEnv), xmlText(stdout), xmlText(stderr)))
}

// supervisedLogLevel is what a freshly onboarded deployment runs at. An
// operator raises it by editing the generated plist and reloading the unit;
// this constant is what `mc __onboard supervision` puts back.
const supervisedLogLevel = "info"

func ensureOwnerDirectory(path, label string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create %s: %w", label, err)
	}
	return requireOwnerOnlyDirectory(path, label)
}

func validateSupervisionTree(root string) error {
	expectedDirs := map[string]bool{".": true, "LaunchAgents": true}
	expectedFiles := map[string]bool{}
	for _, rel := range supervisionFiles {
		expectedFiles[rel] = true
	}
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		uid, links, ok := statOwner(info)
		if !ok || int(uid) != os.Getuid() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("supervision entry %q lost operator ownership or became a symlink", rel)
		}
		if entry.IsDir() {
			if !expectedDirs[rel] || info.Mode().Perm() != 0o700 {
				return fmt.Errorf("supervision contains unexpected or non-owner-only directory %q", rel)
			}
			return nil
		}
		if !expectedFiles[rel] || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || links != 1 || info.Size() == 0 {
			return fmt.Errorf("supervision contains unexpected or unsafe file %q", rel)
		}
		seen[rel] = true
		return nil
	})
	if err != nil {
		return err
	}
	for _, rel := range supervisionFiles {
		if !seen[rel] {
			return fmt.Errorf("supervision is missing %q", rel)
		}
	}
	return nil
}

func supervisionTreesEqual(left, right string) (bool, error) {
	for _, rel := range supervisionFiles {
		a, err := os.ReadFile(filepath.Join(left, filepath.FromSlash(rel)))
		if err != nil {
			return false, err
		}
		b, err := os.ReadFile(filepath.Join(right, filepath.FromSlash(rel)))
		if err != nil {
			return false, err
		}
		if !bytes.Equal(a, b) {
			return false, nil
		}
	}
	return true, nil
}

func validateLaunchdTCCPath(canonicalHome string, protectedRoots []string) error {
	if canonicalHome == "" || !filepath.IsAbs(canonicalHome) ||
		filepath.Clean(canonicalHome) != canonicalHome {
		return fmt.Errorf("launchd MC_HOME must be a clean absolute path")
	}
	for _, root := range protectedRoots {
		if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return fmt.Errorf("launchd TCC root must be a clean absolute path")
		}
		rel, err := filepath.Rel(root, canonicalHome)
		if err != nil {
			return fmt.Errorf("compare MC_HOME with macOS TCC root %q: %w", root, err)
		}
		if rel == "." || (rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return fmt.Errorf(
				"MC_HOME %q is under macOS TCC-protected %q; choose a deployment home outside Documents, Desktop, and Downloads before preparing launchd supervision",
				canonicalHome, root)
		}
	}
	return nil
}

// requireLaunchdTCCSafeHome applies the launchd execution boundary that the sleep drill observed.
// Full Disk Access on the interactive harness does not transfer to a
// LaunchAgent, so a deployment rooted in these user folders is unusable even
// when onboarding itself can read it.
func requireLaunchdTCCSafeHome(canonicalHome string) error {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve operator home for launchd TCC gate: %w", err)
	}
	userHome, err = CanonicalHome(userHome)
	if err != nil {
		return fmt.Errorf("resolve operator home for launchd TCC gate: %w", err)
	}
	roots := make([]string, 0, 3)
	for _, name := range []string{"Documents", "Desktop", "Downloads"} {
		root, err := CanonicalHome(filepath.Join(userHome, name))
		if err != nil {
			return fmt.Errorf("resolve macOS TCC root %s: %w", name, err)
		}
		roots = append(roots, root)
	}
	return validateLaunchdTCCPath(canonicalHome, roots)
}

// PrepareSupervision atomically publishes the exact native configs and three
// per-user LaunchAgent definitions. It deliberately never writes into
// ~/Library/LaunchAgents and never bootstraps a job: activation is the one
// operator-present action. Every label must be absent before and after
// publication so this preparation cannot race an already supervised process.
func PrepareSupervision(home string, spec SupervisionSpec, loaded LaunchdLoadedProbe) (string, SupervisionResult, error) {
	var result SupervisionResult
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return "", result, err
	}
	if err := requireLaunchdTCCSafeHome(canonicalHome); err != nil {
		return "", result, err
	}
	if err := requireOwnerOnlyDirectory(canonicalHome, "MC_HOME"); err != nil {
		return "", result, err
	}
	if !validProductionBuildID(spec.ReleaseBuildID) {
		return "", result, fmt.Errorf("production supervision requires a 40..64 lowercase-hex release identity")
	}
	if !validSupervisionImage(spec.Image) {
		return "", result, fmt.Errorf("production supervision requires a verified image digest or a local production image")
	}
	bindHost, bindPort, bindErr := net.SplitHostPort(spec.DashboardBind)
	parsedPort, portErr := strconv.Atoi(bindPort)
	if bindErr != nil || portErr != nil || bindHost == "" || parsedPort < 1 || parsedPort > 65535 {
		return "", result, fmt.Errorf("production supervision requires a valid dashboard host:port")
	}
	if spec.DashboardAuthToken == "" {
		return "", result, fmt.Errorf("production supervision requires a dashboard auth token on every bind, loopback included")
	}
	if err := validateViewerBinds(spec.SupervisionPolicy); err != nil {
		return "", result, err
	}
	if spec.BackupIntervalS <= 0 {
		return "", result, fmt.Errorf("production supervision requires a positive backup interval")
	}
	if spec.TickIntervalS <= 0 {
		return "", result, fmt.Errorf("production supervision requires a positive tick interval")
	}
	if strings.TrimSpace(spec.GitAuthorName) == "" ||
		strings.TrimSpace(spec.GitAuthorEmail) == "" ||
		!strings.Contains(spec.GitAuthorEmail, "@") ||
		len(spec.GitAuthorName) > 256 || len(spec.GitAuthorEmail) > 256 ||
		strings.ContainsAny(spec.GitAuthorName, "\x00\r\n") ||
		strings.ContainsAny(spec.GitAuthorEmail, "\x00\r\n") {
		return "", result, fmt.Errorf("production supervision requires an explicit safe Git identity")
	}
	names, err := RuntimeNames(canonicalHome)
	if err != nil {
		return "", result, err
	}
	if spec.SpineVolume != names.Volume {
		return "", result, fmt.Errorf("supervision spine volume does not match canonical MC_HOME")
	}
	mcPath, err := canonicalExecutable(spec.MCPath, "mc binary")
	if err != nil {
		return "", result, err
	}
	bunPath, err := canonicalExecutable(spec.BunPath, "bun binary")
	if err != nil {
		return "", result, err
	}
	dockerPath, err := canonicalExecutable(spec.DockerPath, "docker binary")
	if err != nil {
		return "", result, err
	}
	roots, err := validateWorkspaceRoots(spec.WorkspaceRoots)
	if err != nil {
		return "", result, err
	}
	runnerRoot := filepath.Join(canonicalHome, "release", "runner")
	hostRoot := filepath.Join(canonicalHome, "release", "host")
	if err := ValidateRunnerRelease(runnerRoot); err != nil {
		return "", result, fmt.Errorf("validate installed runner release: %w", err)
	}
	if err := ValidateHostRelease(hostRoot); err != nil {
		return "", result, fmt.Errorf("validate installed host release: %w", err)
	}

	result = SupervisionResultFor(canonicalHome, names)
	if loaded == nil {
		return "", result, fmt.Errorf("launchd loaded-state probe is required")
	}
	labels := result.Labels()
	for _, label := range labels {
		isLoaded, err := loaded(label)
		if err != nil {
			return "", result, fmt.Errorf("inspect LaunchAgent %s: %w", label, err)
		}
		if isLoaded {
			return "", result, fmt.Errorf("LaunchAgent %s is already loaded; refuse offline configuration replacement", label)
		}
	}
	for _, item := range []struct{ path, label string }{
		{filepath.Join(canonicalHome, "logs"), "log root"},
		{filepath.Join(canonicalHome, "behaviors"), "behavior root"},
	} {
		if err := ensureOwnerDirectory(item.path, item.label); err != nil {
			return "", result, err
		}
	}
	agentsViewToken, err := EnsureAgentsViewToken(canonicalHome)
	if err != nil {
		return "", result, err
	}
	wikiKey, err := EnsureWikiKey(canonicalHome)
	if err != nil {
		return "", result, err
	}

	workspaceRoot := ""
	if len(roots) > 0 {
		workspaceRoot = roots[0].Root
	}
	resident := map[string]any{
		"agentCmd":          []string{RuntimeRoot + "/bin/bun", RuntimeRoot + "/app/src/agent-runner/main.ts"},
		"agentRunnerRoutes": []string{}, "behaviorsDir": filepath.Join(canonicalHome, "behaviors"),
		"backupIntervalMs":    spec.BackupIntervalS * 1000,
		"tickIntervalMs":      spec.TickIntervalS * 1000,
		"gitAuthorName":       spec.GitAuthorName,
		"gitAuthorEmail":      spec.GitAuthorEmail,
		"configSchemaVersion": 1, "dockerPath": dockerPath,
		"homieCmd": []string{RuntimeRoot + "/bin/bun", RuntimeRoot + "/app/src/homie-runner/main.ts"},
		"image":    spec.Image, "landCmd": []string{"mc-land"}, "mcHome": canonicalHome,
		"mcPath": mcPath, "releaseBuildId": spec.ReleaseBuildID,
		"roleBehaviors": map[string]string{}, "runnerSrcDir": runnerRoot,
		"runtimeBindings": spec.RuntimeBindings,
		"runtimeVolume":   names.RuntimeVolume,
		"projectImage":    spec.ProjectImage,
		"spineDbPath":     "/mc/spine/spine.db", "spineVolume": names.Volume,
		"workspaceRoot": workspaceRoot, "workspaceRoots": roots,
		"agentsViewBind": spec.AgentsViewBind,
	}
	residentJSON, err := json.MarshalIndent(resident, "", "  ")
	if err != nil {
		return "", result, err
	}
	residentJSON = append(residentJSON, '\n')
	dashboardConfigBody := map[string]string{
		"auth_token": spec.DashboardAuthToken, "bind": spec.DashboardBind, "mc_bin": mcPath,
		"wiki_bind": spec.WikiBind, "wiki_key": wikiKey,
		"release_id": spec.ReleaseBuildID,
	}
	dashboardJSON, _ := json.MarshalIndent(dashboardConfigBody, "", "  ")
	dashboardJSON = append(dashboardJSON, '\n')

	stage, err := os.MkdirTemp(canonicalHome, ".supervision-stage-")
	if err != nil {
		return "", result, err
	}
	defer os.RemoveAll(stage)
	if err := os.Mkdir(filepath.Join(stage, "LaunchAgents"), 0o700); err != nil {
		return "", result, err
	}
	residentConfig := filepath.Join(result.Root, "resident.json")
	dashboardConfig := filepath.Join(result.Root, "dashboard.json")
	pathDirs := []string{filepath.Dir(mcPath), filepath.Dir(bunPath), filepath.Dir(dockerPath), "/usr/bin", "/bin", "/usr/sbin", "/sbin"}
	seenPath := map[string]bool{}
	pathEnv := []string{}
	for _, dir := range pathDirs {
		if !seenPath[dir] {
			seenPath[dir] = true
			pathEnv = append(pathEnv, dir)
		}
	}
	logRoot := filepath.Join(canonicalHome, "logs")
	// The generated level, not the generating operator's. Onboarding must
	// produce the same plist whether or not the shell that ran it happened to
	// carry MC_LOG_LEVEL, or a debug-level install would be reproducible only
	// by accident.
	logLevel := supervisedLogLevel
	residentPlist := launchAgent(result.ResidentLabel, bunPath,
		filepath.Join(hostRoot, "resident", "src", "main.ts"), residentConfig,
		filepath.Join(hostRoot, "resident"), canonicalHome, logLevel, strings.Join(pathEnv, ":"),
		filepath.Join(logRoot, "resident.stdout.log"), filepath.Join(logRoot, "resident.stderr.log"))
	dashboardPlist := launchAgent(result.DashboardLabel, bunPath,
		filepath.Join(hostRoot, "dashboard", "src", "main.ts"), dashboardConfig,
		filepath.Join(hostRoot, "dashboard"), canonicalHome, logLevel, strings.Join(pathEnv, ":"),
		filepath.Join(logRoot, "dashboard.stdout.log"), filepath.Join(logRoot, "dashboard.stderr.log"))
	agentsViewPlistBody, agentsViewConfigBody, err := agentsViewBundle(canonicalHome, names, spec.AgentsViewBind, agentsViewToken)
	if err != nil {
		return "", result, err
	}
	for rel, body := range map[string][]byte{
		"resident.json": residentJSON, "dashboard.json": dashboardJSON,
		"LaunchAgents/resident.plist": residentPlist, "LaunchAgents/dashboard.plist": dashboardPlist,
		agentsViewPlist: agentsViewPlistBody, agentsViewConfig: agentsViewConfigBody,
	} {
		if err := writeReleaseFile(stage, rel, body); err != nil {
			return "", result, fmt.Errorf("stage supervision file %q: %w", rel, err)
		}
	}
	if err := syncDirectory(filepath.Join(stage, "LaunchAgents")); err != nil {
		return "", result, err
	}
	if err := syncDirectory(stage); err != nil {
		return "", result, err
	}
	if err := validateSupervisionTree(stage); err != nil {
		return "", result, err
	}

	status := "done"
	if info, statErr := os.Lstat(result.Root); statErr == nil {
		uid, _, ok := statOwner(info)
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !ok || int(uid) != os.Getuid() {
			return "", result, fmt.Errorf("supervision root must remain a real operator-owned directory")
		}
		if err := validateSupervisionTree(result.Root); err == nil {
			equal, err := supervisionTreesEqual(stage, result.Root)
			if err != nil {
				return "", result, err
			}
			if equal {
				status = "ok"
			} else if err := atomicExchangeDirectories(stage, result.Root); err != nil {
				return "", result, err
			} else if err := os.RemoveAll(stage); err != nil {
				return "", result, err
			}
		} else if err := atomicExchangeDirectories(stage, result.Root); err != nil {
			return "", result, err
		} else if err := os.RemoveAll(stage); err != nil {
			return "", result, err
		}
	} else if errors.Is(statErr, fs.ErrNotExist) {
		if err := os.Rename(stage, result.Root); err != nil {
			return "", result, err
		}
	} else {
		return "", result, statErr
	}
	if err := syncDirectory(canonicalHome); err != nil {
		return "", result, err
	}
	for _, label := range labels {
		isLoaded, err := loaded(label)
		if err != nil {
			return "", result, fmt.Errorf("reinspect LaunchAgent %s: %w", label, err)
		}
		if isLoaded {
			return "", result, fmt.Errorf("LaunchAgent %s became loaded during offline publication", label)
		}
	}
	return status, result, nil
}
