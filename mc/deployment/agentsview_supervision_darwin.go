package deployment

import (
	"fmt"
	"strconv"
)

// Darwin (launchd) half of the AgentsView bundle: the property list renderer
// and the bundle file map the shared comparator expects. The path constants
// stay in the shared file until stage ①b separates the inspect surface.

// agentsViewBundle returns the AgentsView property list and configuration
// that a deployment at canonicalHome prepares for bind and token.
func agentsViewBundle(canonicalHome string, names Names, bind, token string) ([]byte, []byte, error) {
	host, port, err := viewerBind("surfaces.agentsview.bind", bind)
	if err != nil {
		return nil, nil, err
	}
	if !viewerSecretPattern.MatchString(token) {
		return nil, nil, fmt.Errorf("production supervision requires a valid AgentsView token")
	}
	pin, err := LoadAgentsViewPin()
	if err != nil {
		return nil, nil, err
	}
	paths := AgentsViewPathsFor(canonicalHome)
	return agentsViewLaunchAgent(UnitLabel(names, "agentsview"), paths, host, strconv.Itoa(port)),
		agentsViewConfigTOML(paths, host, port, pin.DisabledAgents, token), nil
}

// agentsViewLaunchAgent writes the AgentsView property list. The dead loopback
// proxy blocks the pricing refresh, which has no setting of its own. Go sends
// no loopback request through a proxy.
func agentsViewLaunchAgent(label string, paths AgentsViewPaths, host, port string) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>serve</string>
    <string>--host</string>
    <string>%s</string>
    <string>--port</string>
    <string>%s</string>
    <string>--no-browser</string>
    <string>--no-update-check</string>
  </array>
  <key>WorkingDirectory</key>
  <string>%s</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>AGENTSVIEW_DATA_DIR</key>
    <string>%s</string>
    <key>AGENTSVIEW_DISABLE_UPDATE_CHECK</key>
    <string>1</string>
    <key>AGENTSVIEW_TELEMETRY_ENABLED</key>
    <string>0</string>
    <key>HOME</key>
    <string>%s</string>
    <key>HTTPS_PROXY</key>
    <string>http://127.0.0.1:9</string>
    <key>HTTP_PROXY</key>
    <string>http://127.0.0.1:9</string>
    <key>PATH</key>
    <string>/usr/bin:/bin</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
  <key>ThrottleInterval</key>
  <integer>10</integer>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, xmlText(label), xmlText(paths.Executable), xmlText(host), xmlText(port), xmlText(paths.Home),
		xmlText(paths.Data), xmlText(paths.Home), xmlText(paths.StdoutLog), xmlText(paths.StderrLog)))
}

func platformAgentsViewFiles(canonicalHome string, names Names, bind, token string) (map[string][]byte, error) {
	plist, config, err := agentsViewBundle(canonicalHome, names, bind, token)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{agentsViewPlist: plist, agentsViewConfig: config}, nil
}
