package deployment

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Darwin (launchd) half of the supervision seam. The shared orchestration in
// supervision.go calls only the platform hooks defined here and in
// supervision_linux.go, so a Linux build carries no launchd behavior at all
// beyond inert source text that stage ①b removes from shared errors.

var supervisionFiles = []string{
	"LaunchAgents/agentsview.plist",
	"LaunchAgents/dashboard.plist",
	"LaunchAgents/resident.plist",
	"agentsview.toml",
	"dashboard.json",
	"resident.json",
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

// renderUnitFiles writes the three per-user LaunchAgent definitions plus the
// agentsview pair. It deliberately never touches ~/Library/LaunchAgents:
// publication into the supervision tree is the whole of what prepare does.
func renderUnitFiles(in unitRenderInput) (map[string][]byte, error) {
	residentConfig := filepath.Join(in.Result.Root, "resident.json")
	dashboardConfig := filepath.Join(in.Result.Root, "dashboard.json")
	// The generated level, not the generating operator's. Onboarding must
	// produce the same plist whether or not the shell that ran it happened to
	// carry MC_LOG_LEVEL, or a debug-level install would be reproducible only
	// by accident.
	logLevel := supervisedLogLevel
	residentPlist := launchAgent(in.Result.ResidentLabel, in.BunPath,
		filepath.Join(in.HostRoot, "resident", "src", "main.ts"), residentConfig,
		filepath.Join(in.HostRoot, "resident"), in.CanonicalHome, logLevel, in.PathEnv,
		filepath.Join(in.LogRoot, "resident.stdout.log"), filepath.Join(in.LogRoot, "resident.stderr.log"))
	dashboardPlist := launchAgent(in.Result.DashboardLabel, in.BunPath,
		filepath.Join(in.HostRoot, "dashboard", "src", "main.ts"), dashboardConfig,
		filepath.Join(in.HostRoot, "dashboard"), in.CanonicalHome, logLevel, in.PathEnv,
		filepath.Join(in.LogRoot, "dashboard.stdout.log"), filepath.Join(in.LogRoot, "dashboard.stderr.log"))
	agentsViewPlistBody, agentsViewConfigBody, err := agentsViewBundle(in.CanonicalHome, in.Names, in.Spec.AgentsViewBind, in.AgentsViewToken)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		"LaunchAgents/resident.plist":  residentPlist,
		"LaunchAgents/dashboard.plist": dashboardPlist,
		agentsViewPlist:                agentsViewPlistBody,
		agentsViewConfig:               agentsViewConfigBody,
	}, nil
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

// precheckSupervisionHome is the launchd home gate; every other supervision
// input check is platform-neutral and lives in PrepareSupervision.
func precheckSupervisionHome(canonicalHome string) error {
	return requireLaunchdTCCSafeHome(canonicalHome)
}
